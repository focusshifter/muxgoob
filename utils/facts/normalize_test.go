package facts

import "testing"

func TestNormalizePersonFacts_PreservesCurrentFactsForMetaOutput(t *testing.T) {
	current := "Identity:\n- works in IT\n\nInterests:\n- likes metal\n- often shares music links"
	candidate := "**Обновление фактов о slimcheg:**\n\nНовых устойчивых фактов не появилось. Текущее сообщение не добавляет долговременной информации."

	got := NormalizePersonFacts(current, candidate)
	if got != current {
		t.Fatalf("expected current facts to be preserved, got %q", got)
	}
}

func TestNormalizePersonFacts_RejectsSuspiciouslyShortRewrite(t *testing.T) {
	current := "Identity:\n- works in IT and talks about tooling\n\nInterests:\n- likes metal and hardcore\n- frequently shares music links and album takes\n- discusses concerts, festivals, and audio production"
	candidate := "likes music"

	got := NormalizePersonFacts(current, candidate)
	if got != current {
		t.Fatalf("expected current facts to be preserved, got %q", got)
	}
}

func TestNormalizePersonFacts_AllowsRealUpdatedDossier(t *testing.T) {
	current := "Identity:\n- works in IT\n\nInterests:\n- likes metal"
	candidate := "Identity:\n- works in IT\n\nInterests:\n- likes metal, hardcore, and experimental music\n- often shares album reviews and concert impressions"

	got := NormalizePersonFacts(current, candidate)
	if got != candidate {
		t.Fatalf("expected updated dossier to be kept, got %q", got)
	}
}

func TestNormalizePersonFacts_EmptyCurrentAndMetaOutputStaysEmpty(t *testing.T) {
	candidate := "No new stable facts."

	got := NormalizePersonFacts("", candidate)
	if got != "" {
		t.Fatalf("expected empty facts, got %q", got)
	}
}

func TestNormalizePersonFacts_RejectsInvalidStructuredRewriteAndPreservesCurrent(t *testing.T) {
	current := "Identity:\n- works in IT\n\nInterests:\n- likes metal"
	candidate := "Identity:\n- works in IT\n\nInterests:\nthis line is not a bullet"

	got := NormalizePersonFacts(current, candidate)
	if got != current {
		t.Fatalf("expected current facts to be preserved, got %q", got)
	}
}
