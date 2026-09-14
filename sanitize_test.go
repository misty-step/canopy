package main

import "testing"

func TestSanitizeLogText(t *testing.T) {
	const longSecret = "supersecretvalue123"
	const longSK = "sk-abcdefghijklmnopqrstuvwx"
	const shortSK19 = "sk-1234567890123456789"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "benign", in: "hello world", want: "hello world"},
		{name: "benign exit", in: "run completed exit 0", want: "run completed exit 0"},
		{name: "keys slash", in: "keys/" + longSecret, want: "keys/[REDACTED]"},
		{name: "key id equals", in: "key_id=" + longSecret, want: "key_id=[REDACTED]"},
		{name: "key id colon", in: "key-id:" + longSecret, want: "key-id:[REDACTED]"},
		{name: "token equals", in: "token=" + longSecret, want: "token=[REDACTED]"},
		{name: "token colon", in: "token:" + longSecret, want: "token:[REDACTED]"},
		{name: "bearer capital", in: "Bearer " + longSecret, want: "Bearer [REDACTED]"},
		{name: "bearer lower", in: "bearer " + longSecret, want: "bearer [REDACTED]"},
		{name: "api key equals", in: "api_key=" + longSecret, want: "api_key=[REDACTED]"},
		{name: "api key colon", in: "api-key:" + longSecret, want: "api-key:[REDACTED]"},
		{name: "standalone sk", in: longSK, want: "[REDACTED]"},
		{name: "case insensitive token", in: "TOKEN=" + longSecret, want: "TOKEN=[REDACTED]"},
		{name: "short token nonmatch", in: "token=short", want: "token=short"},
		{name: "short bearer nonmatch", in: "Bearer short", want: "Bearer short"},
		{name: "short token boundary eleven", in: "token=abcdefghijk", want: "token=abcdefghijk"},
		{name: "token boundary twelve", in: "token=abcdefghijkl", want: "token=[REDACTED]"},
		{name: "short sk nonmatch", in: "sk-short", want: "sk-short"},
		{name: "short sk nineteen nonmatch", in: shortSK19, want: shortSK19},
		{name: "sk twenty match", in: "sk-12345678901234567890", want: "[REDACTED]"},
		{
			name: "repeated",
			in:   "token=" + longSecret + " and token=" + longSecret,
			want: "token=[REDACTED] and token=[REDACTED]",
		},
		{
			name: "multiline",
			in:   "line1 token=" + longSecret + "\nline2 Bearer " + longSecret,
			want: "line1 token=[REDACTED]\nline2 Bearer [REDACTED]",
		},
		{
			name: "embedded preserves surrounding",
			in:   "prefix keys/abc123XYZ456789 suffix",
			want: "prefix keys/[REDACTED] suffix",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeLogText(tc.in); got != tc.want {
				t.Fatalf("sanitizeLogText(%q)=%q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
