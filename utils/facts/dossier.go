package facts

import "strings"

type Dossier struct {
	Identity  []string
	Interests []string
}

func ParseDossier(text string) *Dossier {
	d := &Dossier{}
	var section *[]string

	for _, rawLine := range strings.Split(normalizeWhitespace(text), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		switch line {
		case "Identity:":
			section = &d.Identity
			continue
		case "Interests:":
			section = &d.Interests
			continue
		}

		if strings.HasSuffix(line, ":") {
			section = nil
			continue
		}

		if section == nil {
			continue
		}

		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			item := strings.TrimSpace(line[2:])
			if item != "" {
				*section = append(*section, item)
			}
		}
	}

	return d
}

func RenderDossier(d *Dossier) string {
	if d == nil {
		d = &Dossier{}
	}

	var out strings.Builder
	writeSection(&out, "Identity:", d.Identity)
	out.WriteString("\n\n")
	writeSection(&out, "Interests:", d.Interests)
	return strings.TrimSpace(out.String())
}

func writeSection(out *strings.Builder, heading string, items []string) {
	out.WriteString(heading)
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out.WriteString("\n- ")
		out.WriteString(item)
	}
}
