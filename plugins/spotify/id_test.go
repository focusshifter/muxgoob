package spotify

import "testing"

func TestExtractSpotifyID(t *testing.T) {
	testCases := []struct {
		name    string
		input   string
		wantID  string
		wantErr bool
	}{
		{
			name:   "raw id",
			input:  "2OhKXbQFehTRfFnYz1J95v",
			wantID: "2OhKXbQFehTRfFnYz1J95v",
		},
		{
			name:   "album url",
			input:  "https://open.spotify.com/album/2OhKXbQFehTRfFnYz1J95v",
			wantID: "2OhKXbQFehTRfFnYz1J95v",
		},
		{
			name:   "track url with query",
			input:  "https://open.spotify.com/track/1234567890abcdef?si=abc123",
			wantID: "1234567890abcdef",
		},
		{
			name:   "intl path url",
			input:  "https://open.spotify.com/intl-en/album/2OhKXbQFehTRfFnYz1J95v",
			wantID: "2OhKXbQFehTRfFnYz1J95v",
		},
		{
			name:   "single quoted url",
			input:  "'https://open.spotify.com/album/2OhKXbQFehTRfFnYz1J95v'",
			wantID: "2OhKXbQFehTRfFnYz1J95v",
		},
		{
			name:   "spotify uri",
			input:  "spotify:album:2OhKXbQFehTRfFnYz1J95v",
			wantID: "2OhKXbQFehTRfFnYz1J95v",
		},
		{
			name:    "unsupported host",
			input:   "https://example.com/album/2OhKXbQFehTRfFnYz1J95v",
			wantErr: true,
		},
		{
			name:    "invalid value",
			input:   "not-a-spotify-id",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractSpotifyID(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got id: %s", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantID {
				t.Fatalf("expected id %q, got %q", tc.wantID, got)
			}
		})
	}
}
