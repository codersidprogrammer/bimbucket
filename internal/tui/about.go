package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	appName    = "bimbucket"
	authorName = "Mochammad Dimas Editiya"
	appTagline = "Bitbucket Server 6.7 -> Bitbucket Cloud migrator"
)

// Version is the build version, injected by the entrypoint.
var Version = "dev"

var (
	logoMuted  = lipgloss.Color("244")
	logoBright = lipgloss.Color("15")
	logoShadow = lipgloss.Color("236")
)

// logoFont is a 4-row block font in the opencode wordmark style. Each glyph is
// four columns wide; shadow markers are '_' (shadow cell), '^' (letter top over
// shadow), '~' (shadow top) and ',' (shadow base).
var logoFont = map[rune][4]string{
	'b': {"▄   ", "█▀▀▄", "█__█", "▀▀▀▀"},
	'i': {" ▄  ", " █  ", " █  ", "▀▀▀▀"},
	'm': {"    ", "█▄█ ", "█ █ ", "▀ ▀ "},
	'u': {"    ", "█  █", "█__█", "▀▀▀▀"},
	'c': {"    ", "█▀▀▀", "█___", "▀▀▀▀"},
	'k': {"▄   ", "█▄▀ ", "█ ▀▄", "▀  ▀"},
	'e': {"    ", "█▀▀█", "█^^^", "▀▀▀▀"},
	't': {" ▄  ", "▀█▀ ", " █  ", " ▀▀ "},
}

func logoRows(word string) []string {
	lines := make([]string, 4)
	for i, r := range word {
		g, ok := logoFont[r]
		if !ok {
			continue
		}
		for row := 0; row < 4; row++ {
			if i > 0 {
				lines[row] += " "
			}
			lines[row] += g[row]
		}
	}
	return lines
}

func renderLogoLine(s string, fg lipgloss.Color) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '_':
			b.WriteString(lipgloss.NewStyle().Background(logoShadow).Render(" "))
		case '^':
			b.WriteString(lipgloss.NewStyle().Foreground(fg).Background(logoShadow).Bold(true).Render("▀"))
		case '~':
			b.WriteString(lipgloss.NewStyle().Foreground(logoShadow).Render("▀"))
		case ',':
			b.WriteString(lipgloss.NewStyle().Foreground(logoShadow).Render("▄"))
		case ' ':
			b.WriteString(" ")
		default:
			b.WriteString(lipgloss.NewStyle().Foreground(fg).Bold(true).Render(string(r)))
		}
	}
	return b.String()
}

// wordmarkLines renders "bimbucket" in two tones: "bimb" muted, "ucket" bright.
func wordmarkLines() []string {
	left := logoRows("bimb")
	right := logoRows("ucket")
	lines := make([]string, len(left))
	for i := range left {
		lines[i] = renderLogoLine(left[i], logoMuted) + "  " + renderLogoLine(right[i], logoBright)
	}
	return lines
}

func (m *Model) viewAbout() string {
	var b strings.Builder
	b.WriteString("\n")
	for _, line := range wordmarkLines() {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("\n")
	b.WriteString("  " + titleStyle.Render(appName) + " " + mutedStyle.Render(Version) + "  " + mutedStyle.Render(appTagline) + "\n")
	b.WriteString("\n")
	b.WriteString("  built by " + goodStyle.Render(authorName) + "\n")
	return b.String()
}
