# Automatic selfprompt consolidation

Ordinary person-profile and chat-guidance updates extract a delta, retain the existing safety/sanitization checks, apply it, then normalize whitespace and remove exact duplicate bullets locally. Automatic normalization has no section-count quota, fuzzy-overlap merging, or clipping. Existing semantic delta filters remain separate from normalization.

`selfprompt.ShouldConsolidateMemory` requests a serial LLM consolidation only when the trimmed, merged candidate is **at least 16 × 1024 bytes**. Bullet count alone never triggers it. This is a conservative large-volume safeguard/heuristic, **not** a storage cap, context-window boundary, or model/API limit. A failed shortener may leave a stored candidate above the threshold; retaining facts is preferable to truncation.

A filtered chat delta with no operations, or an effectively unchanged normalized merge, returns the original prompt immediately—even above the safeguard. `NO_CHANGES` also returns without a consolidation call. An ordinary update therefore requires only its extraction call (apart from existing invalid-output retries).

Rare consolidation requests preserve distinct durable details and have no mandatory tiny bullet quotas. Person consolidation retains the existing dossier validation and retry checks; pinned Appearance bullets are restored unchanged before accepting a shorter result. Chat consolidation uses the shared `facts.EvaluateChatPromptConsolidation` validator in both the runtime and `cmd/selfprompt`: it requires all three headings in order, nonempty structured bullet content, a strictly shorter result, retains every populated section, and rejects destructive rewrites that retain fewer than half of an existing four-or-more-bullet prompt. These are structural safety checks, not a semantic proof that an LLM preserved every fact. Invalid/empty/expanding/destructive output and request errors retain the merged candidate. No automatic local compaction follows consolidation.

Provider/model routing, FIFO processing, refresh guards, and message watermarks are unchanged. Structured-memory validation, pinned aliases, and persistence remain owned by their existing layers.

Compatibility: `DefaultCompactChars()` now returns the byte safeguard despite its historical name. `DefaultCompactBullets()` is deprecated and returns maximum `int` rather than a zero sentinel, so legacy `>=` comparisons do not trigger on every update. New callers should use `ShouldConsolidateMemory` directly.

Tests: `mise exec -- go test ./plugins/selfprompt -count=1`. HTTP completions are intercepted with synthetic fixtures; no external model calls or production writes are needed.
