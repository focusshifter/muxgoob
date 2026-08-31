package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestBuildContextExcludesPlansAndScopesPersonFacts(t *testing.T) {
	db := setupRepositoryTestDB(t)
	defer db.Close()
	repo := NewRepository(db)
	ctx := context.Background()
	user1, user2 := int64(1), int64(2)
	entries := []Entry{
		{ChatID: 10, Kind: ChatLore, Body: "Shared ritual", SourceType: "test"},
		{ChatID: 10, Kind: Decision, Body: "Booked Kyoto", SourceType: "test"},
		{ChatID: 10, Kind: PossiblePlan, Body: "Maybe visit Uji", SourceType: "test"},
		{ChatID: 10, Kind: PersonFact, SubjectUserID: &user1, Body: "Likes jazz", SourceType: "test"},
		{ChatID: 10, Kind: PersonFact, SubjectUserID: &user2, Body: "Likes metal", SourceType: "test"},
	}
	for _, entry := range entries {
		if _, _, err := repo.Add(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	rendered, err := repo.BuildContext(ctx, 10, []int64{user1})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Shared ritual", "Booked Kyoto", "Likes jazz"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("missing %q in %q", expected, rendered)
		}
	}
	for _, excluded := range []string{"Maybe visit Uji", "Likes metal"} {
		if strings.Contains(rendered, excluded) {
			t.Fatalf("unexpected %q in %q", excluded, rendered)
		}
	}
}

func TestBuildContextPrioritizesPinnedAppearance(t *testing.T) {
	db := setupRepositoryTestDB(t)
	defer db.Close()
	repo := NewRepository(db)
	ctx := context.Background()
	userID := int64(1)
	if _, _, err := repo.Add(ctx, Entry{ChatID: 10, Kind: PersonFact, SubjectUserID: &userID, Body: "Canonical emo dwarf", Retention: Pinned, SourceType: "test"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxContextEntriesPerKind+5; i++ {
		if _, _, err := repo.Add(ctx, Entry{ChatID: 10, Kind: PersonFact, SubjectUserID: &userID, Body: fmt.Sprintf("recent replaceable fact %d", i), SourceType: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	rendered, err := repo.BuildContext(ctx, 10, []int64{userID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "Canonical emo dwarf") {
		t.Fatalf("pinned appearance was omitted from capped context: %q", rendered)
	}
}
