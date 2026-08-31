package facts

import "testing"

func TestPersonNamesEquivalentSupportsRussianInflectionAndDiminutive(t *testing.T) {
	tests := []struct {
		request, candidate string
		want               bool
	}{
		{"ивана", "Иван", true},
		{"Витя", "Victor", true},
		{"витю", "Виктор", true},
		{"витёк", "viktor", true},
		{"Витя", "Виталий", false},
		{"Витя", "Vasiliy", false},
	}
	for _, tt := range tests {
		if got := PersonNamesEquivalent(tt.request, tt.candidate); got != tt.want {
			t.Errorf("PersonNamesEquivalent(%q, %q) = %v, want %v", tt.request, tt.candidate, got, tt.want)
		}
	}
}
