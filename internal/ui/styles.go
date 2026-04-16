package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── Color Palette (OpenCode-inspired, warm minimal) ─────────────────────────

var (
	ColorAccent  = lipgloss.Color("#fab283") // warm peach/orange — primary accent
	ColorGreen   = lipgloss.Color("#7fd88f")
	ColorRed     = lipgloss.Color("#e06c75")
	ColorYellow  = lipgloss.Color("#f5a742")
	ColorBlue    = lipgloss.Color("#56b6c2")
	ColorPurple  = lipgloss.Color("#9d7cd8")
	ColorDim     = lipgloss.Color("#555555")
	ColorMuted   = lipgloss.Color("#808080")
	ColorWhite   = lipgloss.Color("#eeeeee")
	ColorBorder  = lipgloss.Color("#2a2a2a")
	ColorPanel   = lipgloss.Color("#141414")
	ColorBg      = lipgloss.Color("#0a0a0a")
)

// ── Status Icons ────────────────────────────────────────────────────────────

var (
	IconSuccess = lipgloss.NewStyle().Foreground(ColorGreen).Render("•")
	IconError   = lipgloss.NewStyle().Foreground(ColorRed).Render("•")
	IconWarn    = lipgloss.NewStyle().Foreground(ColorYellow).Render("△")
	IconPending = lipgloss.NewStyle().Foreground(ColorBlue).Render("○")
	IconArrow   = lipgloss.NewStyle().Foreground(ColorAccent).Render("→")
	IconDot     = lipgloss.NewStyle().Foreground(ColorDim).Render("·")
)

// ── Text Styles ─────────────────────────────────────────────────────────────

var (
	Bold      = lipgloss.NewStyle().Bold(true)
	Dim       = lipgloss.NewStyle().Foreground(ColorDim)
	Muted     = lipgloss.NewStyle().Foreground(ColorMuted)
	Subtle    = lipgloss.NewStyle().Foreground(ColorDim).Italic(true)
	Highlight = lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	Success   = lipgloss.NewStyle().Foreground(ColorGreen)
	Error     = lipgloss.NewStyle().Foreground(ColorRed)
	Warn      = lipgloss.NewStyle().Foreground(ColorYellow)
	Info      = lipgloss.NewStyle().Foreground(ColorBlue)
	White     = lipgloss.NewStyle().Foreground(ColorWhite)
	Accent    = lipgloss.NewStyle().Foreground(ColorAccent)
)

// ── Section & Header Styles ─────────────────────────────────────────────────

var (
	SectionTitle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Bold(true).
			MarginTop(1)

	StepStyle = lipgloss.NewStyle().
			Foreground(ColorAccent).
			Bold(true)
)

// ── Box & Panel Styles ─────────────────────────────────────────────────────

var (
	Panel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorBorder).
		Padding(1, 2)

	SuccessPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorGreen).
			Padding(1, 2)

	ErrorPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorRed).
			Padding(1, 2)

	WarnPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorYellow).
			Padding(1, 2)

	InfoPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(1, 2)
)

// ── Table Styles ────────────────────────────────────────────────────────────

var (
	TableHeader = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Bold(true).
			PaddingRight(2)

	TableCell = lipgloss.NewStyle().
			PaddingRight(2)

	TableCellDim = lipgloss.NewStyle().
			Foreground(ColorDim).
			PaddingRight(2)
)

// ── Layout ──────────────────────────────────────────────────────────────────

const MaxWidth = 90

func Centered(width int, content string) string {
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, content)
}

func ContentWidth(termWidth int) int {
	if termWidth > MaxWidth {
		return MaxWidth
	}
	return termWidth
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func StatusIcon(ok bool) string {
	if ok {
		return IconSuccess
	}
	return IconError
}

func StatusText(status string) string {
	switch status {
	case "verified", "enabled", "active":
		return Success.Render(status)
	case "pending", "not_started":
		return Info.Render(status)
	case "failed", "disabled", "error":
		return Error.Render(status)
	default:
		return Dim.Render(status)
	}
}

func KeyValue(key, value string) string {
	return Muted.Render(key+": ") + White.Render(value)
}

func Step(msg string) string {
	return IconArrow + " " + StepStyle.Render(msg)
}

func StepResult(icon, msg string) string {
	return "  " + icon + " " + msg
}

func KeyBind(key, label string) string {
	return lipgloss.NewStyle().Foreground(ColorAccent).Render(key) +
		lipgloss.NewStyle().Foreground(ColorDim).Render(" "+label)
}

func Banner() string {
	return Accent.Bold(true).Render("mailctl") + Dim.Render(" — custom domain email manager")
}

// MaskEmail replaces the local part of an email with stars for display.
// e.g. "user@gmail.com" -> "u***@gmail.com"
func MaskEmail(email string) string {
	at := strings.Index(email, "@")
	if at <= 0 {
		return email
	}
	local := email[:at]
	domain := email[at:]
	if len(local) <= 1 {
		return local + "***" + domain
	}
	return string(local[0]) + "***" + domain
}
