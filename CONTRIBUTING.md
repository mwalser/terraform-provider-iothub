# Contributing

## Development

```sh
go build ./...                                    # build
go test ./...                                     # unit tests
make lint                                         # golangci-lint
make generate                                     # regenerate docs/ from schema + examples/

# acceptance tests against a real hub (Entra ID via `az login`, or SAS via IOTHUB_CONNECTION_STRING)
TF_ACC=1 IOTHUB_TEST_HOSTNAME=<hub>.azure-devices.net go test ./internal/provider/ -run TestAcc -v -timeout 60m
```

The design, every decision and the service behaviour verified against a live
hub live in [`CONCEPT.md`](CONCEPT.md). Change the concept when you change a
behaviour; record newly verified service facts in its Appendix D.

## Documentation rules

User-facing documentation is generated from the schema descriptions
(`MarkdownDescription`), `templates/` and `examples/`. It describes the
**contract** the provider offers — not how it is implemented, and not how we
know the service behaves the way it does. Both of those belong in `CONCEPT.md`.

Keep:

- What the object is, in the user's vocabulary.
- For every attribute: meaning, valid values, default, sensitivity, whether a
  change is in place or **forces replacement** — and the consequence when that
  is surprising (e.g. a replaced configuration re-targets every device).
- What the user has to supply and what they get back (`jsondecode()` on JSON
  attributes, import IDs, where reported properties live).
- What the user needs to make it work: credentials, roles and policy
  permissions, phrased as instructions; hard authentication caveats.
- Behavioural promises that shape configuration: twin leaf-path ownership,
  keys in state vs. write-only, `wait`, `expected_status_codes`, when an
  action fails.
- One `~>` note per page at most, and only for a genuine gotcha.

Leave out:

- REST paths, HTTP methods, status codes and service error codes; ETag,
  `If-Match`, retry, polling and refresh mechanics.
- Evidence phrasing ("verified", "the service answers …", "the hub silently …").
- Tiers, quotas, rates, limits and prices — link Azure's documentation where a
  limit changes what the user must do.
- Design rationale ("by design", "no knob", "lossless", references to
  `CONCEPT.md` sections).
- Internal plan-time mechanics. One sentence saying that a value is checked
  at plan time is fine when it saves the user an apply; how the check works
  is not.
- Azure's own file formats beyond a pointer.

Style: a one- or two-sentence summary, then the attributes. Plain language
over service jargon ("if the device is offline", not "404 `DeviceNotOnline`").
`CHANGELOG.md` follows the same rule: what changed for the user, not why or
how it was found.

**Write plain sentences.** One idea per sentence, in the order the reader
needs it: what it is, what it does, what to watch out for. Use active voice
and everyday words. If a sentence needs a dash, a semicolon or nested
parentheses to hold together, split it into two. Keep parentheses for short
asides like `(default true)`. Use backticks for names and bold only for a
warning the reader must not miss. Read the paragraph aloud; if you run out of
breath, it is too long. The `name — description` form in lists and tables is
list punctuation, not a sentence, and stays.
