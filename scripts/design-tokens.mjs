import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { lint } from "@google/design.md/linter";
import postcss from "postcss";

const root = new URL("../", import.meta.url);
const source = new URL("DESIGN.md", root);
const target = new URL("static/design-tokens.css", root);
const mode = process.argv[2];
if (!["--write", "--check"].includes(mode) || process.argv.length !== 3) {
  throw new Error("Usage: node scripts/design-tokens.mjs --write|--check");
}

// Use the pinned upstream parser and reference resolver, not a second YAML schema.
const report = lint(await readFile(source, "utf8"));
if (report.summary.errors) {
  console.error(JSON.stringify({ findings: report.findings, summary: report.summary }, null, 2));
  throw new Error("DESIGN.md has errors; token output was not written");
}
const tokens = new Map();
const kebab = (name) => name.replace(/([a-z])([A-Z])/g, "$1-$2").replaceAll(".", "-").toLowerCase();
const hexByte = (value) => Math.round(value).toString(16).padStart(2, "0");

function emit(name, value) {
  if (value?.type === "typography") {
    for (const [property, part] of Object.entries(value)) {
      if (property !== "type" && part !== undefined) emit(`${name}-${kebab(property)}`, part);
    }
    return;
  }
  let css;
  if (value?.type === "color") {
    css = `#${hexByte(value.r)}${hexByte(value.g)}${hexByte(value.b)}`;
    if (value.a !== undefined && value.a < 1) css += hexByte(value.a * 255);
  } else if (value?.type === "dimension") {
    css = `${value.value}${value.unit}`;
  } else if (typeof value === "string" || typeof value === "number") {
    css = String(value);
  } else {
    throw new Error(`Cannot emit ${name} as a CSS value`);
  }
  const property = `--${kebab(name)}`;
  if (!/^--[a-z][a-z0-9-]*$/.test(property) || tokens.has(property) || /[;{}\n\r]/.test(css)) {
    throw new Error(`Invalid or duplicate CSS token ${property}`);
  }
  tokens.set(property, css);
}

for (const [group, prefix] of [["colors", ""], ["typography", ""], ["rounded", "radius-"], ["spacing", "space-"]]) {
  for (const [name, value] of report.designSystem[group]) emit(`${prefix}${name}`, value);
}
for (const [name, component] of report.designSystem.components) {
  for (const [property, value] of component.properties) emit(`component-${name}-${kebab(property)}`, value);
}
if (!tokens.size) throw new Error("DESIGN.md did not yield any CSS tokens");
const generated = "/* Generated from DESIGN.md by npm run design:generate. Do not edit. */\n:root {\n" +
  [...tokens].map(([name, value]) => `  ${name}: ${value};`).join("\n") + "\n}\n";

if (mode === "--write") {
  await writeFile(target, generated);
  console.log(`Generated ${tokens.size} plain CSS custom properties in static/design-tokens.css`);
} else {
  console.log(JSON.stringify({ findings: report.findings, summary: report.summary }, null, 2));
  if (await readFile(target, "utf8") !== generated) {
    throw new Error("static/design-tokens.css is out of date; run npm run design:generate");
  }

  const stylesheet = new URL("static/canopy.css", root);
  const css = postcss.parse(await readFile(stylesheet, "utf8"), { from: fileURLToPath(stylesheet) });
  const known = new Set(tokens.keys());
  const failures = [];
  const fail = (node, message) => failures.push(`${node.source.start.line}: ${message}`);
  css.walkDecls((declaration) => {
    if (!declaration.prop.startsWith("--")) return;
    if (tokens.has(declaration.prop)) fail(declaration, `generated token ${declaration.prop} is overridden`);
    known.add(declaration.prop);
  });
  css.walkDecls((declaration) => {
    const value = declaration.value;
    for (const match of value.matchAll(/var\(\s*(--[a-zA-Z0-9-]+)/g)) {
      if (!known.has(match[1])) fail(declaration, `unknown token ${match[1]}`);
    }
    // Ignore token identifiers, not var() fallbacks, when checking literal colors.
    const withoutTokenNames = value.replace(/--[a-zA-Z0-9-]+/g, "");
    if (/#(?:[0-9a-f]{8}|[0-9a-f]{6}|[0-9a-f]{4}|[0-9a-f]{3})\b|\b(?:rgba?|hsla?|hwb|oklch|oklab|lch|lab|color|color-mix)\s*\(|\b(?:white|black|red|green|blue|gray|grey)\b/i.test(withoutTokenNames)) {
      fail(declaration, "literal color belongs in DESIGN.md");
    }
    const fontFace = declaration.parent.type === "atrule" && declaration.parent.name === "font-face";
    if (!fontFace && ["font", "font-family"].includes(declaration.prop) && !value.includes("var(") && !["inherit", "initial", "unset", "revert"].includes(value)) {
      fail(declaration, "font stack must reference DESIGN.md tokens");
    }
  });
  // Browser chrome and the standalone favicon cannot inherit :root properties.
  // Keep their small format-required color mirrors tied to the same authority.
  const page = await readFile(new URL("templates/page.html", root), "utf8");
  const icon = await readFile(new URL("static/canopy.svg", root), "utf8");
  if (page.match(/<meta name="theme-color" content="([^"]+)"/)?.[1].toLowerCase() !== tokens.get("--canvas")) failures.push("page theme-color differs from colors.canvas");
  if (icon.match(/<svg\b[^>]*\bfill="([^"]+)"/)?.[1].toLowerCase() !== tokens.get("--primary")) failures.push("favicon fill differs from colors.primary");
  if (failures.length) throw new Error(`Design conformance failed:\n${failures.join("\n")}`);
  console.log("Generated tokens and hand-authored CSS conform to DESIGN.md");
}
