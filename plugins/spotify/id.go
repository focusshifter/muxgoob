package spotify

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var spotifyIDRegex = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// ExtractSpotifyID returns a Spotify track/album ID from either a raw ID or URL.
func ExtractSpotifyID(input string) (string, error) {
	candidate := strings.TrimSpace(input)
	candidate = strings.Trim(candidate, `"'`)
	if candidate == "" {
		return "", fmt.Errorf("empty spotify id")
	}

	// Raw ID.
	if spotifyIDRegex.MatchString(candidate) {
		return candidate, nil
	}

	// Spotify URI format.
	if strings.HasPrefix(candidate, "spotify:") {
		parts := strings.Split(candidate, ":")
		if len(parts) == 3 && (parts[1] == "album" || parts[1] == "track") && spotifyIDRegex.MatchString(parts[2]) {
			return parts[2], nil
		}
		return "", fmt.Errorf("invalid spotify uri")
	}

	parsedURL, err := url.Parse(candidate)
	if err != nil {
		return "", fmt.Errorf("invalid spotify url")
	}
	if parsedURL.Hostname() != "open.spotify.com" {
		return "", fmt.Errorf("unsupported spotify host")
	}

	pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
	for i := 0; i < len(pathParts)-1; i++ {
		kind := pathParts[i]
		if kind != "album" && kind != "track" {
			continue
		}

		id := strings.TrimSpace(pathParts[i+1])
		if spotifyIDRegex.MatchString(id) {
			return id, nil
		}
		return "", fmt.Errorf("invalid spotify id in url")
	}

	return "", fmt.Errorf("spotify url must contain /album/<id> or /track/<id>")
}
