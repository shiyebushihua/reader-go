# Engine and importer implementation notes

## Contract and scope

Exported interfaces in `CONTRACT.md` are unchanged. These packages have no database, upload ownership, or API dependencies. This is a bounded static-source subset, **not full Legado compatibility**. No JavaScript interpreter, browser, XPath engine, or arbitrary plugin execution is present. This completion did not edit any `go.mod`.

Dependencies used by the existing module: goquery 1.10.3, cascadia (CSS matching), x/net 0.40.0, and x/text 0.25.0. Source uses Go 1.24+ APIs; validation here used the supplied Go 1.27.3 Windows/amd64 toolchain.

## Supported online-source subset

- `bookSourceUrl`, `searchUrl`, and static `header` (JSON string map or a string containing such a map).
- Supported nonempty rule fields:
  - `ruleSearch`: `bookList`, `name`, `author`, `bookUrl`, `coverUrl`, `intro`.
  - `ruleBookInfo`: `init`, `name`, `author`, `coverUrl`, `intro`, `tocUrl`.
  - `ruleToc`: `chapterList`, `chapterName`, `chapterUrl`, `nextTocUrl`.
  - `ruleContent`: `content`, `nextContentUrl`, `replaceRegex`.
- HTML: cascadia CSS selectors, optional `css:`/`@css:` prefix; chained `@` descendant selections; legacy `class.name`, `id.name`, `tag.name`, optionally followed by a zero-based positive/negative index. Scalar suffixes select attributes or `text`, `ownText`, `textNodes`, `html`, `all`.
- Important approximation: `text`, `textNodes`, `html`, and `all` all produce normalized plain text, **not original HTML**. `ownText` extracts direct child text. Script/style/noscript/iframe/object/SVG subtrees are removed; paragraph-like blocks and line breaks preserve readable line boundaries. CSS layout is not evaluated.
- JSON: `$`, dot keys, simple nonempty quoted bracket keys, nonnegative array indices, and array `[*]`. List selection flattens selected arrays; scalar selection accepts strings, numbers, booleans, and null (empty). Object scalars error. No recursive descent, filters, slices, unions, object wildcards, escaped/empty quoted keys, or negative JSON array indices.
- `||` selects the first nonempty result; `&&` combines lists or joins scalar strings with newlines. `##pattern##replacement` (or `##pattern` for deletion) uses Go RE2 semantics and Go replacement syntax, not Java regex. Cleaning is applied after the expression. Delimiters are simple string separators, not an escaping-aware grammar.
- URL templates support only `{{key}}`, `{key}`, `{{page}}`, `{page}`. Search fetches **page 1 only**. Keyword substitutions use UTF-8 form/query escaping in URLs and bodies, not arbitrary expression evaluation or charset-specific query encoding.
- URL suffix `,{...}` / `, {...}` options support `method` (GET/POST only), `header`/`headers`, `body`, and response `charset`. Body may be a string or serialized JSON; substitutions still use form escaping. No browser, retry, signing, or scripting options.
- Relative references resolve against the final response URL; fragments are removed. Duplicate search/chapter URLs and pagination loops are suppressed. TOC and content follow a single next-page URL, up to 10 pages; identical content-page text is deduplicated. No multi-link fanout, reverse-TOC syntax, search pagination, caching, or authenticated login workflow.
- FetchSource validates JSON object/array syntax only; it is a preview fetch, not validation that every imported rule is executable.

### Explicit unsupported behavior

Nonempty unknown fields within the four rule groups are rejected before fetching (including VIP/volume rules, `webJs`, and `sourceRegex`). `jsLib`, `loginUrl`, `loginUi`, `loginCheckJs`, `coverDecodeJs`, and `bookUrlPattern` are rejected when nonempty. `@js:`, `<js>`, `javascript:`, XPath markers, `@get:`, `@put:`, unexpanded double-brace expressions, and `%%` fail explicitly. Invalid CSS/JSONPath/regex and unknown HTTP options also error. A source with unsupported rules in an unused group can therefore still be rejected. Other top-level metadata/discovery settings are ignored; discovery itself is not implemented. Static HTTP headers are supported, but dynamic login/session scripts are not.

## Network security and budgets

- Only absolute HTTP(S) destinations without URL credentials; no environment proxy. Source headers cannot override Host, Proxy-Authorization, or Accept-Encoding.
- Literal addresses and every DNS answer are checked against private, loopback, link-local, multicast, reserved/documentation, and selected IPv6 transition ranges. Mixed public/private DNS answers fail closed. Dialing uses the validated literal address, avoiding a second hostname resolution. IPv6 zones are rejected. This is application-layer filtering, not a replacement for egress firewall policy.
- Redirect destinations are revalidated, and the redirected connection uses the same protected dialer. Redirect callback rejects when the prior-request count reaches 5 (at most four followed redirects). Authorization/Cookie/Proxy-Authorization are stripped on origin changes or HTTPS downgrade; arbitrary custom secret headers are not classified. Source-configured headers apply again to subsequent explicit page loads, so source definitions must be trusted administrator input.
- Eight process-wide concurrent fetches; per-fetch 25-second deadline including slot wait, 10-second dial/TLS handshake, 15-second response-header timeout, 64 KiB response headers. Search deadline 60 seconds; details/content 90 seconds.
- 1 MiB source JSON and request body; 8 MiB wire/decompressed/decoded response; identity/gzip only; non-2xx errors. UTF-8, GBK/GB2312/GB18030, Big5, supported x/net charset labels, and HTML meta charset detection. Charset controls response decoding only.
- 500 search results; 20,000 list items/chapters (including combined lists); 10 TOC/content pages; 16 MiB extracted chapter chunks before joining separators. Empty content/TOC extraction errors instead of returning a fake success.

## Local imports

`Parse` accepts an existing controlled regular local file; original filename chooses TXT/EPUB and supplies fallback title. The caller remains responsible for safe upload creation and cleanup. No archive member is extracted to disk and no external resource is fetched.

- TXT: UTF-8 with optional BOM, otherwise GBK/GB18030 decoding; rejects undecodable input, NUL/binary/UTF-16-style input, and empty text. Normalizes line endings. Chinese chapter headings and English Chapter/Book/Part numeric/Roman headings split chapters; heading text remains in content. Unstructured text and very long lines split at UTF-8 boundaries. No TXT author inference or universal encoding detection.
- EPUB: ZIP/container/first OPF rootfile, first title and joined creators, manifest plus linear HTML/XHTML spine order. EPUB3 nav labels and nested EPUB2 NCX labels are supported (nav labels take precedence). Falls back to h1/h2/h3, HTML title, then generated title. Duplicate spine files are suppressed; `linear="no"` entries and empty chapters are skipped.
- EPUB content is normalized plain text. Scripts, styles, active embedded content, head, hidden and aria-hidden subtrees are omitted. No images, covers, CSS/layout, embedded fonts, audio, raw HTML rendering, fragment-level chapter splitting, DRM, or encrypted/obfuscated EPUB support. Nav fragments label whole files, not subchapters. This is not a complete EPUB conformance validator (e.g. mimetype ordering is not enforced).
- ZIP names reject traversal, absolute/drive/backslash/NUL paths and noncanonical names. References URL-decode paths and reject external URLs, queries, absolute paths, or escape above archive root; legitimate relative links within the archive are allowed. Duplicate regular entries, symlinks/nonregular files, encryption, missing referenced content, duplicate manifest IDs, and unsupported spine media types error.
- Limits: 100 MiB upload; 64 MiB TXT/decoded text or EPUB extracted text; 4 MiB chapter; 20,000 chapters; 10,000 ZIP entries; 128 MiB declared archive total and cumulative decompression budget; 1 MiB container, 2 MiB OPF, 8 MiB each read nav/NCX/chapter. Entries above 1 MiB with compression ratio over 200:1 are rejected, including unused assets. This intentionally conservative rule may reject unusually repetitive valid books. Actual reads are bounded independently of ZIP metadata. Cancellation is checked during reads and processing loops, not at every parser operation.

## Completion fixes and verification

Concrete fixes: existing 2 MiB repetitive ZIP fixture bypassed the former 1000:1 ratio check; tightened to 200:1 with non-rounded comparison. Combined list expressions now enforce their aggregate cap. JSON documents reject trailing garbage/multiple documents. JSONPath unions/escaped quoted keys no longer silently become literal missing keys. String-encoded `null` source headers no longer cause a nil-map assignment panic when request headers are merged. Unsupported rule fields and JS-dependent source settings fail before network activity.

Validation commands (from `reader-go`, with supplied scratch toolchain and caches):

```text
go test ./internal/engine ./internal/importer
go test -count=1 -cover ./internal/engine ./internal/importer
go test -count=1 -json ./internal/engine ./internal/importer
go vet ./internal/engine ./internal/importer
```

Final results: **22 top-level tests passed** (12 engine, 10 importer), plus **19 named subtests** (8 engine, 11 importer); zero failures. Coverage: engine **79.3%**, importer **84.2%**. Vet passed. Changed Go files formatted with gofmt. Added 11 top-level tests to the existing 11. Fixtures use custom RoundTrippers, injected resolver/dial functions, a loopback httptest redirect server, and `t.TempDir` archives/files; no real internet is required. Tests cover HTML/JSON end-to-end extraction, options/encoding, SSRF dial pinning and mixed answers, redirects/credential stripping, response/pagination/list budgets, explicit unsupported constructs, malformed JSON, TXT splitting/encoding, EPUB spine/nav/NCX, ZIP traversal/metadata/read limits/encryption/symlinks and missing content. MySQL/backend integration was not rerun in this scoped completion; the parent reported its TXT integration already passed. Race detection and exhaustive fuzzing were not run.

## Parent integration follow-up

Real source-file smoke testing exposed overly strict display metadata rejection. `kind`, `lastChapter`, `wordCount`, `checkKeyWord` in search/info, and `imageStyle` in content are now ignored without evaluating their expressions. This version does not display these fields. Other unsupported executable/content-affecting fields remain rejected. Added `TestDisplayMetadataDoesNotBlockReading`; scoped engine/importer tests passed after this fix. The preceding completion test counts describe the delegate's checkpoint, before this additional regression test.

User-supplied file was inspected without copying it into the repository: 188 sources, static markers in 28 for JS and 6 for XPath (heuristics, not certified capability classification). Five conservative real-source candidates were attempted after the fix: one empty result, one nonstandard single-quoted HTTP-options JSON failure, three network/request failures. No real-source end-to-end success has yet been verified. Fixture success is not evidence of real-source compatibility.
