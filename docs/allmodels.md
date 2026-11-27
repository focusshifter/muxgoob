# Optional AllModels chat provider

AllModels is **explicit-only**. Installing this code or adding its key does not
activate a model, migrate settings, or change the default dispatcher: OpenAI GPT
models still use Codex, non-OpenAI IDs still use OrcaRouter, and Codex's existing
OpenRouter error fallback is unchanged. Main chat and memory workloads are not
reconfigured by this integration.

## Credentials and privacy

Securely put `allmodels_api_key` in the bot's YAML configuration and restart to
load it. This key is separate from `orcarouter_api_key`, `openrouter_api_key`,
OpenAI API keys, and Codex OAuth. Missing credentials fail before network IO;
AllModels calls never borrow another key or fall back to another router/model.
The endpoint is `https://api.allmodels.io/oai/chat/completions`, with Bearer auth.
There are no automatic retries, redirects, or provider-key logs. Requests have a
120-second deadline and responses have a 4 MiB limit.

**Every AllModels request explicitly sends `provider: {"zdr": true}`.** The
[official provider documentation](https://allmodels.io/llm/docs/providers)
(and its Markdown version `https://allmodels.io/llm/docs/providers.md`) documents
that this excludes non-ZDR providers from *all* upstream capacity failovers and
returns `404 no_matching_provider` rather than using a retaining provider when
no eligible route exists. We do not pin an arbitrary provider or add price/sort
preferences. This conservative adapter policy is not an editable DB setting and
is never relaxed on an error. AllModels' own policy and any hosted-search
subprocessor policy still need independent review; a ZDR routing flag is not a
claim of independently audited retention behavior.

The requested model's catalog has both ZDR and retaining upstreams, and normally
routes cheapest-first. A cheapest route being ZDR today is not a substitute for
an explicit ZDR constraint on future failovers.

## Owner commands: scoped activation

These are proposed commands, **not changes made by installing the code**. Use the
configured bot owner's private chat and the exact target chat ID. Set the target
chat's reply model without changing its default provider:

```text
!ai model reply allmodels:deepseek/deepseek-v4.1-flash-abliterated <private_chat_id>
!ai get <private_chat_id>
!ai chat <private_chat_id>
```

The upstream ID is exactly `deepseek/deepseek-v4.1-flash-abliterated`. Do not
substitute an Orca model with a similar name. No `:online` suffix is needed for
plain activation; do not silently append it to a user's selection.

Alternatively, `!ai provider allmodels <chat_id>` explicitly selects AllModels
for that chat, and a plain model ID then stays on AllModels. This broader change
also affects workloads that inherit the chat provider. The model prefix is
narrower, but is **not** a promise that every other workload stays unchanged:
selfprompt/compression and Spotify writers can inherit `ai_model` when they have
no explicit model, and topics helpers intentionally use the reply model. Before
activation, inspect the effective scoped/global settings and pin any inherited
workload that must retain its current model. For example, preserve the effective
Luna selfprompt model rather than mistaking a stale legacy compression row for
an active override. Do not claim isolation based on raw DB rows alone.

Independent workload examples:

```text
!ai model vision allmodels:<vision-capable-model> <chat_id>
!ai model selfprompt allmodels:<text-model> <chat_id>
!ai provider image-prompt allmodels <chat_id>
!ai model image-prompt deepseek/deepseek-v4.1-flash-abliterated <chat_id>
!spotify model openai/gpt-6.1-sol allmodels:deepseek/deepseek-v4.1-flash-abliterated
```

Selfprompt plugin/CLI and compression resolve their own effective model;
existing setting precedence is preserved. Topics helpers, the ordinary reply
tool loop, incoming-image vision, image prompt composition, Spotify authors and
the judge use the same route helper. Spotify provenance retains the full
configured model token. **Spotify Sonar published-criticism research remains
OpenRouter-only**, with its original key and citation adapter.

The `allmodels:` routing prefix is split exactly once, before Codex/OpenRouter
model normalization. Native slash IDs and nonterminal model variants remain
opaque. An empty prefixed ID is an error. The wrapper removes only a terminal
`:online`; all other plain model IDs are sent unchanged.

## Hosted web search: optional

Proposed optional command, only when search is requested:

```text
!ai model reply allmodels:deepseek/deepseek-v4.1-flash-abliterated:online <private_chat_id>
```

The adapter strips terminal `:online`, sends the same model to Chat Completions,
and adds `web_search_options: {"search_context_size": "medium"}`. It preserves
host function tools, tool-result continuation, structured output, and vision
parts. It does **not** inject an Orca search tool, call Orca's standalone search,
or layer another search provider over AllModels. Each adapter call issues one
POST. Hosted search may incur provider charges on each completion/continuation;
this is not an account-wide search budget.

Evidence and remaining limitation:

- [Official quickstart](https://allmodels.io/llm/docs) documents the `/oai`
  Chat Completions endpoint, messages, and function tools.
- [Official provider docs](https://allmodels.io/llm/docs/providers) explicitly
  mention Chat Completions `:online` (including after a routing variant).
- The [exact model card](https://allmodels.io/llm/models/deepseek/deepseek-v4.1-flash-abliterated)
  advertises `web_search_options` and image/tool input support.
- The [router page](https://allmodels.io/llm/abliterated) explicitly advertises
  hosted web search on Responses, but not a complete Chat search request/response
  example. The public docs found do **not** fully spell out Chat search's nested
  options or URL-annotation guarantee. The standard OpenAI-compatible medium
  context shape is implemented and mock-tested, but **must be verified against
  the real account before treating Chat `:online` as operational**. Do not claim
  that mock responses prove hosted search, or silently route failed search to
  Orca/offline completion. Plain chat routing does not depend on search.

## Empty-response diagnostics

An HTTP success with an empty/whitespace final `content` is not a successful
answer. The adapter reports `ErrEmptyCompletion`; ordinary replies display a
short failure notice instead of silently disappearing. This does not retry the
completion, switch providers, relax ZDR, expose reasoning as an answer, or invoke
an image-composer fallback. Ordinary `tool_calls` completions remain valid.

The `[allmodels]` diagnostic records only bounded/sanitized response identifiers,
upstream provider, finish reason, choice/tool counts, known message-field
presence/lengths and token counters. It never records prompts, final text,
reasoning/refusal text, raw bodies or credentials. A terminal response with tool
calls but an unexpected finish reason is diagnosed, not executed automatically.
These fields help distinguish future incidents; old `reason=stop with empty
content` logs do not retrospectively prove refusal, reasoning exhaustion or a
specific upstream failure.

## Image generation is not supported

AllModels is allowed for chat and the **text/tool prompt composer**, not as an
image-generation provider. `!ai provider image allmodels` is rejected. A routed
`allmodels:` image-generation model also fails before IO rather than accidentally
falling through to Codex. Vision is image *input*, not image generation.

## Verification and unpublished opt-in smoke

Normal tests intercept HTTP and do not require credentials or contact providers:

```bash
GOFLAGS=-tags=sqlite_fts5 mise exec -- go test ./...
GOFLAGS=-tags=sqlite_fts5 mise exec -- go vet ./...
GOFLAGS=-tags=sqlite_fts5 mise exec -- go build ./...
```

Supply `ALLMODELS_API_KEY` securely through the environment, not command history.
Then explicitly opt into the unpublished smoke (paid calls may occur):

```bash
ALLMODELS_LIVE_TEST=1 ALLMODELS_TEST_MODEL=deepseek/deepseek-v4.1-flash-abliterated GOFLAGS=-tags=sqlite_fts5 mise exec -- go test ./internal/allmodels -run '^TestAllModelsLiveSmoke$' -count=1 -v
```

Both key and flag are required. The model environment variable is an exact plain
upstream ID, not a routed/suffixed ID. The smoke has an overall 120-second
context and performs up to three bounded calls: forced inert local tool,
continuation with its result, and hosted search. Every request keeps ZDR enabled.
Search passes only with provider-originated URL-citation annotations captured
from the HTTP response, not plausible prose or model-written links. Nothing is
sent to Telegram, saved to production settings/DB, or published as a review.
Until this test passes on a real account, live availability, host tools and
hosted search remain unverified. On November 27, 2026, the exact
`deepseek/deepseek-v4.1-flash-abliterated` model passed this unpublished smoke:
host-tool invocation and result continuation succeeded, and hosted search
returned two provider URL-citation annotations. All requests retained the ZDR
filter. This does not certify other models/accounts, live vision, structured
schema enforcement or an independent audit of provider retention.
