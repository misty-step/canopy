package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Source credentials never enter inventory or a rendered snapshot. Each name
// resolves only inside the server-side HTTP boundary, never from a browser.
type ReadSource struct {
	Endpoint string `json:"endpoint"`
	TokenEnv string `json:"token_env"`
}

type HabitatSource struct {
	ReadSource
	System      string   `json:"system"`
	WorkItemIDs []string `json:"work_item_ids,omitempty"`
}

type ForgeSource struct {
	ReadSource
	WebURL          string           `json:"web_url"`
	AutomationLogin string           `json:"automation_login,omitempty"`
	Candidates      []ForgeCandidate `json:"candidates,omitempty"`
}

// ForgeCandidate is explicit operator scope, not review or tracker evidence.
type ForgeCandidate struct {
	URL      string  `json:"url"`
	Work     WorkRef `json:"work"`
	Branch   string  `json:"branch"`
	Revision string  `json:"revision"`
}

type TicketSources struct {
	Habitat *HabitatSource `json:"habitat,omitempty"`
	Forge   *ForgeSource   `json:"forge,omitempty"`
}

const sourceRefreshInterval = time.Minute

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateSourceEndpoint(raw string, private bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(raw, "#") {
		return fmt.Errorf("endpoint must be an absolute URL without credentials, query or fragment")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	local := host == "localhost" || (ip != nil && ip.IsLoopback())
	privateHost := strings.HasSuffix(host, ".internal") || (ip != nil && ip.IsPrivate())
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (local || (private && privateHost))) {
		return fmt.Errorf("endpoint requires HTTPS (HTTP is allowed only on loopback or the private observation network)")
	}
	return nil
}

func validateReadSource(source ReadSource) error {
	if err := validateSourceEndpoint(source.Endpoint, false); err != nil {
		return err
	}
	if !environmentName.MatchString(source.TokenEnv) {
		return fmt.Errorf("token_env must name a credential environment variable, not contain a credential")
	}
	return nil
}

func validateTicketSources(sources TicketSources, observerURL, observerTokenEnv string) error {
	if sources.Habitat != nil {
		if err := validateReadSource(sources.Habitat.ReadSource); err != nil {
			return fmt.Errorf("habitat: %w", err)
		}
		if strings.TrimSpace(sources.Habitat.System) == "" {
			return fmt.Errorf("habitat.system must exactly identify the tracker in Forest work provenance")
		}
		if len(sources.Habitat.WorkItemIDs) > 1000 {
			return fmt.Errorf("habitat.work_item_ids exceeds the 1000-item bounded inventory")
		}
		seen := make(map[string]bool, len(sources.Habitat.WorkItemIDs))
		for _, id := range sources.Habitat.WorkItemIDs {
			if err := validateRouteIdentifier(id, "habitat work item id"); err != nil {
				return err
			}
			if seen[id] {
				return fmt.Errorf("habitat.work_item_ids contains duplicate %q", id)
			}
			seen[id] = true
		}
	}
	if sources.Forge != nil {
		if err := validateReadSource(sources.Forge.ReadSource); err != nil && !isObserverForgeSource(sources.Forge.ReadSource, observerURL, observerTokenEnv) {
			return fmt.Errorf("forge: %w", err)
		}
		if err := validateSourceEndpoint(sources.Forge.WebURL, false); err != nil {
			return fmt.Errorf("forge.web_url: %w", err)
		}
		web, _ := url.Parse(sources.Forge.WebURL)
		if strings.Trim(web.Path, "/") != "" {
			return fmt.Errorf("forge.web_url must be a web origin")
		}
		for _, candidate := range sources.Forge.Candidates {
			if candidate.Work.System == "" || candidate.Work.ID == "" || candidate.Branch == "" || !revisionSHA.MatchString(candidate.Revision) {
				return fmt.Errorf("forge candidate requires immutable work identity, branch and exact revision")
			}
			pr, err := url.Parse(candidate.URL)
			if err != nil || pr.Scheme != web.Scheme || pr.Host != web.Host || pr.User != nil || pr.RawQuery != "" || pr.Fragment != "" {
				return fmt.Errorf("forge candidate URL must belong to the configured web origin")
			}
		}
		if sources.Forge.AutomationLogin != "" {
			// GitHub App logins have a literal terminal [bot] suffix. Validate
			// only a local view: keep the complete identity for receipt matching
			// and the existing safety rules, without imposing user signup rules
			// on App slugs. Any other brackets remain unsafe.
			login := strings.TrimSuffix(sources.Forge.AutomationLogin, "[bot]")
			if err := validateRouteIdentifier(login, "forge automation login"); err != nil {
				return err
			}
		}
	}
	return nil
}

// The observer is a read capability, not a way to forward its credential to an
// arbitrary private API. The sole exception to remote HTTPS is its fixed forge
// metadata proxy, on the exact observation origin with the same read token.
func isObserverForgeSource(source ReadSource, observerURL, observerTokenEnv string) bool {
	if observerURL == "" || source.TokenEnv != observerTokenEnv || !environmentName.MatchString(source.TokenEnv) {
		return false
	}
	if validateSourceEndpoint(source.Endpoint, true) != nil {
		return false
	}
	forge, err := url.Parse(source.Endpoint)
	if err != nil {
		return false
	}
	observer, err := url.Parse(observerURL)
	if err != nil {
		return false
	}
	return forge.Scheme == "http" && forge.Scheme == observer.Scheme && forge.Host == observer.Host &&
		forge.Path == "/v1/github" && observer.Path == "/v1/forest/observe"
}

type sourceReader struct {
	client *http.Client
	lookup func(string) (string, bool)
}

func newSourceReader() sourceReader {
	return sourceReader{
		client: &http.Client{
			Timeout: 8 * time.Second,
			// Even a same-host redirect can cross a credential's API boundary.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		lookup: os.LookupEnv,
	}
}

func (reader sourceReader) read(ctx context.Context, method, endpoint, tokenEnv, header string, body any, target any) error {
	token, ok := reader.lookup(tokenEnv)
	if !ok || strings.TrimSpace(token) == "" {
		return fmt.Errorf("credential environment variable %s is unavailable", tokenEnv)
	}
	if strings.ContainsAny(token, "\r\n") {
		return fmt.Errorf("credential environment variable %s is invalid", tokenEnv)
	}
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode source request")
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("invalid source request")
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if header == "Authorization" {
		token = "Bearer " + token
	}
	request.Header.Set(header, token)
	response, err := reader.client.Do(request)
	if err != nil {
		// Do not forward transport URLs, upstream response bodies or credentials.
		return fmt.Errorf("source request failed or timed out")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("source returned HTTP %d", response.StatusCode)
	}
	const maxResponse = 16 << 20
	contents, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(contents) > maxResponse {
		return fmt.Errorf("source response unavailable or exceeds 16 MiB")
	}
	if err := json.Unmarshal(contents, target); err != nil {
		return fmt.Errorf("source returned invalid JSON")
	}
	return nil
}

func observeCommand(ctx context.Context, instance Instance, args []string) (CommandResult, error) {
	var response struct {
		Stdout   *string `json:"stdout"`
		Stderr   *string `json:"stderr"`
		ExitCode *int    `json:"exit_code"`
	}
	reader := newSourceReader()
	err := reader.read(ctx, http.MethodPost, instance.ObserverURL, instance.ObserverTokenEnv, "Authorization", struct {
		Args []string `json:"args"`
	}{Args: args}, &response)
	if err != nil {
		return CommandResult{Exit: -1}, err
	}
	if response.Stdout == nil || response.Stderr == nil || response.ExitCode == nil {
		return CommandResult{Exit: -1}, fmt.Errorf("observation response is missing process evidence")
	}
	return CommandResult{Stdout: []byte(*response.Stdout), Stderr: []byte(*response.Stderr), Exit: *response.ExitCode}, nil
}

func (c *cliCollector) collectRunHistory(ctx context.Context, instance Instance) RunHistory {
	result := RunHistory{}
	cursor := ""
	seenCursors := make(map[string]bool)
	seenRuns := make(map[string]RunData)
	for {
		args := []string{"run", "list", "--limit", "1000"}
		if cursor != "" {
			args = append(args, "--after", cursor)
		}
		raw, err := c.runJSON(ctx, instance, "run list", args)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		var page struct {
			Runs      []RunData `json:"runs"`
			NextAfter *string   `json:"next_after"`
		}
		if err := decodeCommandData(raw, "run list", &page); err != nil || page.NextAfter == nil {
			result.Error = "Run history pagination is unavailable"
			return result
		}
		for _, run := range page.Runs {
			if len(result.Runs) >= 100000 {
				result.Error = "Run history exceeds the 100000-row observation limit"
				return result
			}
			if run.RunID == "" {
				result.Error = "Run history contains an unattributable row without a Run ID"
				return result
			}
			if previous, exists := seenRuns[run.RunID]; exists {
				if !reflect.DeepEqual(previous, run) {
					result.Error = "Run history contains conflicting duplicate Run IDs"
					return result
				}
				continue
			}
			seenRuns[run.RunID] = run
			result.Runs = append(result.Runs, run)
		}
		cursor = *page.NextAfter
		if cursor == "" {
			result.ObservedAt = time.Now().UTC()
			return result
		}
		if seenCursors[cursor] || len(page.Runs) == 0 {
			result.Error = "Run history pagination did not advance"
			return result
		}
		seenCursors[cursor] = true
	}
}

func (reader sourceReader) collect(ctx context.Context, snapshot Snapshot, previous DeliverySources) DeliverySources {
	result := DeliverySources{}
	ids := snapshotRunIDs(snapshot)
	config := snapshot.Instance.Sources
	if config.Habitat != nil {
		result.Habitat = reader.habitat(ctx, *config.Habitat, snapshot, ids, previous.Habitat)
	}
	result = retainDeliverySources(result, previous)
	if config.Forge != nil {
		result.Forge = reader.forge(ctx, *config.Forge, snapshot.Config, result.Habitat, previous.Forge)
	}
	return retainDeliverySources(result, previous)
}

func (reader sourceReader) habitat(ctx context.Context, source HabitatSource, snapshot Snapshot, ids []string, previous HabitatObservation) HabitatObservation {
	result := HabitatObservation{Items: make(map[string]HabitatItem)}
	for _, id := range source.WorkItemIDs {
		result.Items[id] = HabitatItem{ID: id}
	}
	if len(ids) == 0 && len(result.Items) == 0 {
		result.Error = "No Run IDs or configured work items to query; tracker evidence has not been observed"
		return result
	}
	for offset := 0; offset < len(ids); offset += 50 {
		batch := ids[offset:min(offset+50, len(ids))]
		allowed := make(map[string]bool, len(batch))
		for _, id := range batch {
			allowed[id] = true
		}
		pageOffset := 0
		for {
			params := url.Values{"run_ids": {strings.Join(batch, ",")}, "limit": {"100"}, "offset": {strconv.Itoa(pageOffset)}}
			var page struct {
				Links      []HabitatLink `json:"links"`
				Count      *int          `json:"count"`
				HasMore    *bool         `json:"has_more"`
				NextOffset *int          `json:"next_offset"`
				Scope      struct {
					Kind            string   `json:"kind"`
					ModuleIDs       []string `json:"module_ids"`
					IncludesDeleted *bool    `json:"includes_deleted"`
				} `json:"scope"`
			}
			endpoint := strings.TrimRight(source.Endpoint, "/") + "/api/work/run-links?" + params.Encode()
			if err := reader.read(ctx, http.MethodGet, endpoint, source.TokenEnv, "Authorization", nil, &page); err != nil {
				result.Error = err.Error()
				return result
			}
			if page.Count == nil || page.HasMore == nil || page.Links == nil || page.Scope.IncludesDeleted == nil || *page.Scope.IncludesDeleted ||
				(page.Scope.Kind != "all_modules" && page.Scope.Kind != "authorized_modules") ||
				(*page.HasMore && (page.NextOffset == nil || *page.NextOffset != pageOffset+len(page.Links) || len(page.Links) == 0)) ||
				(!*page.HasMore && pageOffset+len(page.Links) != *page.Count) {
				result.Error = "Habitat link pagination or visibility scope is incomplete"
				return result
			}
			result.Scope = "Current nondeleted items; " + strings.ReplaceAll(page.Scope.Kind, "_", " ")
			if page.Scope.Kind == "authorized_modules" {
				result.Scope += " (" + strings.Join(page.Scope.ModuleIDs, ", ") + ")"
			}
			for _, link := range page.Links {
				if !allowed[link.RunID] || link.WorkItemID == "" || (link.Relationship != "served" && link.Relationship != "created") ||
					(link.Item != nil && link.Item.ID != link.WorkItemID) {
					result.Error = "Habitat returned a link outside the exact Run/item query"
					return result
				}
				result.Links = append(result.Links, link)
				if link.Item != nil {
					result.Items[link.WorkItemID] = *link.Item
				}
			}
			if !*page.HasMore {
				break
			}
			pageOffset = *page.NextOffset
		}
	}
	visitRunWork(snapshot, func(_ string, work WorkRef) {
		if work.System == source.System {
			if _, exists := result.Items[work.ID]; !exists {
				result.Items[work.ID] = HabitatItem{ID: work.ID, Key: work.Key, URL: work.URL}
			}
		}
	})
	itemIDs := make([]string, 0, len(result.Items))
	for id := range result.Items {
		itemIDs = append(itemIDs, id)
	}
	sort.Strings(itemIDs)
	for _, id := range itemIDs {
		item := result.Items[id]
		var response struct {
			Item *HabitatItem `json:"item"`
		}
		endpoint := strings.TrimRight(source.Endpoint, "/") + "/api/work/items/" + url.PathEscape(id)
		if err := reader.read(ctx, http.MethodGet, endpoint, source.TokenEnv, "Authorization", nil, &response); err != nil {
			if old, exists := previous.Items[id]; exists {
				item = old
			}
			item.Error = err.Error()
			result.Items[id] = item
			continue
		}
		if response.Item == nil || response.Item.ID != id || response.Item.Key == "" || response.Item.Status == "" {
			if old, exists := previous.Items[id]; exists {
				item = old
			}
			item.Error = "Habitat item identity or current state is missing"
			result.Items[id] = item
			continue
		}
		item = *response.Item
		item.ObservedAt = time.Now().UTC()
		item.URL = strings.TrimRight(source.Endpoint, "/") + "/work/" + url.PathEscape(item.Key)
		// PR references come only from explicit tracker fields, never text search,
		// branch naming or Done. History recovers earlier merges after reopening.
		urls := make(map[string]bool)
		if item.PRURL != "" {
			urls[item.PRURL] = true
		}
		for _, oldURL := range previous.Items[id].PRURLs {
			urls[oldURL] = true
		}
		var history struct {
			Rows []struct {
				Field string `json:"field_name"`
				Old   string `json:"old_value"`
				New   string `json:"new_value"`
			} `json:"history"`
		}
		if err := reader.read(ctx, http.MethodGet, endpoint+"/history", source.TokenEnv, "Authorization", nil, &history); err != nil {
			item.HistoryWarning = "PR history unavailable; earliest delivery may be incomplete"
		} else {
			if history.Rows == nil || len(history.Rows) >= 50 {
				item.HistoryWarning = "Habitat exposes at most 50 history changes; earliest delivery may be incomplete"
			}
			for _, row := range history.Rows {
				if row.Field == "pr_url" {
					if row.Old != "" {
						urls[row.Old] = true
					}
					if row.New != "" {
						urls[row.New] = true
					}
				}
			}
		}
		for prURL := range urls {
			item.PRURLs = append(item.PRURLs, prURL)
		}
		sort.Strings(item.PRURLs)
		result.Items[id] = item
	}
	result.ObservedAt = time.Now().UTC()
	if result.Scope == "" {
		result.Scope = "Explicit work item inventory and immutable Run provenance only; no module-wide inventory or admission authority"
	}
	return result
}

func forgePullPath(source ForgeSource, repo, prURL string) (string, bool) {
	web, err := url.Parse(source.WebURL)
	if err != nil {
		return "", false
	}
	pr, err := url.Parse(prURL)
	if err != nil || pr.Scheme != web.Scheme || pr.Host != web.Host || pr.User != nil || pr.RawQuery != "" || pr.Fragment != "" {
		return "", false
	}
	parts := strings.Split(strings.Trim(pr.Path, "/"), "/")
	if len(parts) != 4 || parts[0]+"/"+parts[1] != repo || parts[2] != "pull" {
		return "", false
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil || number < 1 {
		return "", false
	}
	return "/repos/" + repo + "/pulls/" + strconv.Itoa(number), true
}

func (reader sourceReader) forge(ctx context.Context, source ForgeSource, config ConfigData, habitat HabitatObservation, previous ForgeObservation) ForgeObservation {
	result := ForgeObservation{Pulls: make(map[string]PullEvidence)}
	urls := make(map[string]bool)
	for _, candidate := range source.Candidates {
		urls[candidate.URL] = true
	}
	for _, item := range habitat.Items {
		for _, prURL := range item.PRURLs {
			urls[prURL] = true
		}
		if item.PRURL != "" {
			urls[item.PRURL] = true
		}
	}
	if len(urls) == 0 {
		result.Error = "No exact PR links available to query; merge evidence is unknown"
		return result
	}
	ordered := make([]string, 0, len(urls))
	for prURL := range urls {
		ordered = append(ordered, prURL)
	}
	sort.Strings(ordered)
	for _, prURL := range ordered {
		evidence := PullEvidence{URL: prURL}
		path, ok := forgePullPath(source, config.Repo, prURL)
		if !ok {
			evidence.Error = "PR URL is outside the configured forge/repository"
			result.Pulls[prURL] = evidence
			continue
		}
		var response struct {
			Merged   *bool      `json:"merged"`
			State    string     `json:"state"`
			MergedAt *time.Time `json:"merged_at"`
			MergeSHA string     `json:"merge_commit_sha"`
			HTMLURL  string     `json:"html_url"`
			Head     struct {
				SHA string `json:"sha"`
				Ref string `json:"ref"`
			} `json:"head"`
			Base struct {
				Ref  string `json:"ref"`
				Repo struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"base"`
			MergedBy struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"merged_by"`
		}
		endpoint := strings.TrimRight(source.Endpoint, "/") + path
		if err := reader.read(ctx, http.MethodGet, endpoint, source.TokenEnv, "Authorization", nil, &response); err != nil {
			evidence.Error = err.Error()
		} else if response.Merged == nil || response.HTMLURL != prURL || response.Base.Repo.FullName != config.Repo || response.Base.Ref == "" ||
			(response.State != "open" && response.State != "closed") {
			evidence.Error = "Forge returned missing or mismatched PR evidence"
		} else {
			evidence.ObservedAt = time.Now().UTC()
			evidence.State = response.State
			evidence.BaseRef = response.Base.Ref
			evidence.HeadSHA = response.Head.SHA
			evidence.HeadRef = response.Head.Ref
			evidence.Merged = *response.Merged
			evidence.MergedAt = response.MergedAt
			evidence.SHA = response.MergeSHA
			evidence.MergedBy = response.MergedBy.Login
			evidence.MergerType = response.MergedBy.Type
			if evidence.Merged && (evidence.MergedAt == nil || evidence.SHA == "") {
				evidence.Error = "Forge merge timestamp or SHA is missing"
				evidence.Merged = false
			}
		}
		if evidence.Error != "" {
			if old, exists := previous.Pulls[prURL]; exists && !old.ObservedAt.IsZero() {
				message := evidence.Error
				evidence = old
				evidence.Error = message
			}
		} else {
			receipts, err := reader.reviewReceipts(ctx, strings.TrimRight(source.Endpoint, "/")+strings.Replace(path, "/pulls/", "/issues/", 1)+"/comments", source.TokenEnv)
			if err != nil {
				evidence.ReviewSource = previous.Pulls[prURL].ReviewSource
				evidence.ReviewReceipts = previous.Pulls[prURL].ReviewReceipts
				evidence.ReviewSource.Error = "Verifier receipt observation failed: " + err.Error()
			} else {
				evidence.ReviewSource.ObservedAt = time.Now().UTC()
				evidence.ReviewReceipts = receipts
			}
			approvals, err := reader.forgeApprovals(ctx, endpoint, source.TokenEnv, evidence.HeadSHA, evidence.MergedAt)
			if err != nil {
				evidence.ApprovalSource = previous.Pulls[prURL].ApprovalSource
				evidence.Approvals = previous.Pulls[prURL].Approvals
				evidence.ApprovalSource.Error = "Forge account review observation failed: " + err.Error()
				evidence.ApprovalWarning = evidence.ApprovalSource.Error
			} else {
				evidence.ApprovalSource.ObservedAt = time.Now().UTC()
				evidence.Approvals = approvals
			}
		}
		result.Pulls[prURL] = evidence
	}
	for _, pull := range result.Pulls {
		if pull.Error != "" || pull.ReviewSource.Error != "" || pull.ApprovalSource.Error != "" {
			result.Error = "Some PR or review observations are unavailable; inspect ticket warnings"
			break
		}
	}
	result.ObservedAt = time.Now().UTC()
	return result
}

func (reader sourceReader) reviewReceipts(ctx context.Context, endpoint, tokenEnv string) ([]ReviewReceipt, error) {
	receipts := make([]ReviewReceipt, 0)
	seen := make(map[int64]bool)
	for page := 1; ; page++ {
		var comments []struct {
			ID          int64     `json:"id"`
			Body        string    `json:"body"`
			URL         string    `json:"html_url"`
			CreatedAt   time.Time `json:"created_at"`
			UpdatedAt   time.Time `json:"updated_at"`
			Association string    `json:"author_association"`
			User        struct {
				ID    int64  `json:"id"`
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"user"`
		}
		if err := reader.read(ctx, http.MethodGet, endpoint+"?per_page=100&page="+strconv.Itoa(page), tokenEnv, "Authorization", nil, &comments); err != nil {
			return nil, err
		}
		if comments == nil {
			return nil, fmt.Errorf("forge comment collection is missing")
		}
		for _, comment := range comments {
			// The private proxy preserves {} placeholders for unrelated comments
			// so filtering never shortens an upstream page or hides later receipts.
			if !strings.Contains(comment.Body, "forest.review.v1") {
				continue
			}
			if comment.ID > 0 && seen[comment.ID] {
				return nil, fmt.Errorf("forge returned duplicate receipt comment identity")
			}
			seen[comment.ID] = true
			receipt := ReviewReceipt{ID: comment.ID, URL: comment.URL, CreatedAt: comment.CreatedAt, UpdatedAt: comment.UpdatedAt,
				Author: comment.User.Login, AuthorID: comment.User.ID, AuthorType: comment.User.Type, Association: comment.Association}
			var payload struct {
				Schema   string `json:"schema"`
				RunID    string `json:"run_id"`
				WorkID   string `json:"work_id"`
				Revision string `json:"revision"`
				Decision string `json:"decision"`
				Summary  string `json:"summary"`
			}
			body, marked := strings.CutPrefix(strings.TrimSpace(comment.Body), "<!-- forest.review.v1 -->\n")
			decoder := json.NewDecoder(strings.NewReader(body))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&payload); !marked || err != nil || decoder.Decode(new(any)) != io.EOF ||
				payload.Schema != "forest.review.v1" || strings.TrimSpace(payload.RunID) == "" || strings.TrimSpace(payload.WorkID) == "" ||
				!revisionSHA.MatchString(payload.Revision) || (payload.Decision != "approve" && payload.Decision != "changes") || strings.TrimSpace(payload.Summary) == "" {
				receipt.ValidationWarning = "Malformed forest.review.v1 receipt"
			} else {
				receipt.Schema, receipt.RunID, receipt.WorkID = payload.Schema, payload.RunID, payload.WorkID
				receipt.Revision, receipt.Decision, receipt.Summary = payload.Revision, payload.Decision, payload.Summary
			}
			receipts = append(receipts, receipt)
		}
		if len(comments) < 100 {
			return receipts, nil
		}
	}
}

var revisionSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (reader sourceReader) forgeApprovals(ctx context.Context, endpoint, tokenEnv, headSHA string, mergedAt *time.Time) ([]ForgeApproval, error) {
	type review struct {
		State       string    `json:"state"`
		Revision    string    `json:"commit_id"`
		URL         string    `json:"html_url"`
		SubmittedAt time.Time `json:"submitted_at"`
		User        struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"user"`
	}
	latest := make(map[string]review)
	for page := 1; ; page++ {
		var reviews []review
		if err := reader.read(ctx, http.MethodGet, endpoint+"/reviews?per_page=100&page="+strconv.Itoa(page), tokenEnv, "Authorization", nil, &reviews); err != nil {
			return nil, err
		}
		if reviews == nil {
			return nil, fmt.Errorf("forge review collection is missing")
		}
		for _, entry := range reviews {
			if entry.User.Login == "" || entry.SubmittedAt.IsZero() || (mergedAt != nil && entry.SubmittedAt.After(*mergedAt)) {
				continue
			}
			if entry.State != "APPROVED" && entry.State != "CHANGES_REQUESTED" && entry.State != "DISMISSED" {
				continue
			}
			if previous, exists := latest[entry.User.Login]; !exists || entry.SubmittedAt.After(previous.SubmittedAt) {
				latest[entry.User.Login] = entry
			}
		}
		if len(reviews) < 100 {
			break
		}
	}
	approvals := make([]ForgeApproval, 0)
	for _, entry := range latest {
		if entry.State == "APPROVED" && headSHA != "" && entry.Revision == headSHA {
			approvals = append(approvals, ForgeApproval{Author: entry.User.Login, AuthorType: entry.User.Type,
				Revision: entry.Revision, URL: safeExternalURL(entry.URL), SubmittedAt: entry.SubmittedAt})
		}
	}
	sort.Slice(approvals, func(i, j int) bool { return approvals[i].Author < approvals[j].Author })
	return approvals, nil
}
