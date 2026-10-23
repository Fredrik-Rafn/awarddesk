package eva

import (
	"log/slog"
	"net/url"
	"path"
	"strings"
	"unicode"

	"awarddesk/internal/award"
	"golang.org/x/net/html"
)

func classifyAwardAvailabilityStatus(day, icon *html.Node) (available, known bool) {
	status := strings.ToLower(strings.TrimSpace(awardAttr(icon, "alt")))
	switch status {
	case "available":
		return true, true
	case "waiting", "ｗaiting", "unavailable", "not available":
		return false, true
	}

	if status == "" && awardAttr(icon, "src") == "" && awardClass(icon, "icon-flightCompany") && awardClass(icon, "table-flightCompany") && awardText(day) == "" {
		return false, true
	}
	return false, false
}

func awardStatusLabel(value string) string {
	var out []rune
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsPrint(r) {
			out = append(out, r)
			if len(out) == 64 {
				break
			}
		}
	}
	return string(out)
}

func awardStatusImageFilename(source string) string {
	u, err := url.Parse(source)
	if err != nil || u.Path == "" || strings.HasSuffix(u.Path, "/") {
		return ""
	}
	filename := path.Base(u.Path)
	var out strings.Builder
	for _, r := range filename {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
			out.WriteRune(r)
			if out.Len() == 64 {
				break
			}
		}
	}
	return out.String()
}

func logUnknownAwardStatus(q award.Query, icon *html.Node) {
	logAwardStatusDiagnostic(q, "availability", awardAttr(icon, "alt"), awardAttr(icon, "src"))
}

func logAwardStatusDiagnostic(q award.Query, source, label, image string) {
	slog.Warn("Unrecognized EVA award status", "source", source,
		"origin", q.Origin, "destination", q.Destination, "date", q.Date,
		"cabin", q.Cabin, "party", q.Adults,
		"status_label", awardStatusLabel(label),
		"image_filename", awardStatusImageFilename(image))
}
