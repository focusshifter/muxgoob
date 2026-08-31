package memory

import "testing"

func TestIsPinnedAppearance(t *testing.T) {
	tests := []struct {
		name  string
		entry Entry
		want  bool
	}{
		{"pinned appearance", Entry{Retention: Pinned, SourceType: "owner_confirmed_appearance"}, true},
		{"pinned alias", Entry{Retention: Pinned, SourceType: "stable_alias_owner_confirmed"}, false},
		{"pinned identity", Entry{Retention: Pinned, SourceType: "stable_identity_owner_confirmed"}, false},
		{"ordinary fact", Entry{Retention: Replaceable, SourceType: "tool"}, false},
	}
	for _, tt := range tests {
		if got := IsPinnedAppearance(tt.entry); got != tt.want {
			t.Errorf("%s: IsPinnedAppearance() = %v, want %v", tt.name, got, tt.want)
		}
	}
}
