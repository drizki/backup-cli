// Package schedule parses the strict schedule mini-language into a systemd
// OnCalendar expression, a biweekly gate flag and (where possible) a cron line.
//
// Accepted forms:
//
//	daily,HH:MM
//	weekly,<day>,HH:MM
//	biweekly,<day>,HH:MM
//	monthly,<dom 1-28>,HH:MM
//	oncalendar:<systemd calendar expression>
package schedule

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Result is a parsed schedule.
type Result struct {
	Spec         string // spec as typed (trimmed)
	Kind         string // daily, weekly, biweekly, monthly, oncalendar
	OnCalendar   string // systemd OnCalendar value
	BiweeklyGate bool   // run must be skipped when the last backup is < 13 days old
	// Cron holds the five cron time fields ("M H dom mon dow"). It is empty
	// for oncalendar specs, which cannot be translated to cron.
	Cron string
}

// Options tune parsing.
type Options struct {
	// Analyze, when non-nil, additionally validates oncalendar expressions
	// (typically by running `systemd-analyze calendar`).
	Analyze func(expr string) error
}

var (
	timeRe      = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)
	calendarRe  = regexp.MustCompile(`^[A-Za-z0-9 *,.:/~-]+$`)
	maxCalendar = 200
)

var days = map[string]struct {
	cal  string
	cron int
}{
	"mon": {"Mon", 1}, "monday": {"Mon", 1},
	"tue": {"Tue", 2}, "tuesday": {"Tue", 2},
	"wed": {"Wed", 3}, "wednesday": {"Wed", 3},
	"thu": {"Thu", 4}, "thursday": {"Thu", 4},
	"fri": {"Fri", 5}, "friday": {"Fri", 5},
	"sat": {"Sat", 6}, "saturday": {"Sat", 6},
	"sun": {"Sun", 0}, "sunday": {"Sun", 0},
}

// Parse validates spec strictly.
func Parse(spec string, opts Options) (Result, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Result{}, errors.New("schedule is empty")
	}
	if k, expr, ok := strings.Cut(spec, ":"); ok && strings.EqualFold(k, "oncalendar") {
		return parseCalendar(spec, expr, opts)
	}
	kind, rest, hasRest := strings.Cut(spec, ",")
	kind = strings.ToLower(kind)
	var parts []string
	if hasRest {
		parts = strings.Split(rest, ",")
	}
	res := Result{Spec: spec, Kind: kind}
	var err error
	switch kind {
	case "daily":
		err = parseDaily(&res, parts)
	case "weekly", "biweekly":
		err = parseWeekly(&res, parts)
	case "monthly":
		err = parseMonthly(&res, parts)
	default:
		err = fmt.Errorf("unknown schedule %q (use daily, weekly, biweekly, monthly or oncalendar:<expr>)", kind)
	}
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

func parseDaily(res *Result, parts []string) error {
	if len(parts) != 1 {
		return errors.New("daily schedule must be daily,HH:MM")
	}
	h, m, err := parseTime(parts[0])
	if err != nil {
		return err
	}
	res.OnCalendar = fmt.Sprintf("*-*-* %02d:%02d:00", h, m)
	res.Cron = fmt.Sprintf("%d %d * * *", m, h)
	return nil
}

func parseWeekly(res *Result, parts []string) error {
	if len(parts) != 2 {
		return fmt.Errorf("%s schedule must be %s,<day>,HH:MM", res.Kind, res.Kind)
	}
	d, ok := days[strings.ToLower(parts[0])]
	if !ok {
		return fmt.Errorf("unknown day %q (use mon..sun or full day names)", parts[0])
	}
	h, m, err := parseTime(parts[1])
	if err != nil {
		return err
	}
	res.OnCalendar = fmt.Sprintf("%s *-*-* %02d:%02d:00", d.cal, h, m)
	res.Cron = fmt.Sprintf("%d %d * * %d", m, h, d.cron)
	res.BiweeklyGate = res.Kind == "biweekly"
	return nil
}

func parseMonthly(res *Result, parts []string) error {
	if len(parts) != 2 {
		return errors.New("monthly schedule must be monthly,<day-of-month 1-28>,HH:MM")
	}
	dom, err := strconv.Atoi(parts[0])
	if err != nil || strings.TrimSpace(parts[0]) != parts[0] {
		return fmt.Errorf("invalid day of month %q", parts[0])
	}
	if dom >= 29 && dom <= 31 {
		return fmt.Errorf("day of month %d is not allowed: not every month has it, use 1-28", dom)
	}
	if dom < 1 || dom > 28 {
		return fmt.Errorf("day of month %d out of range 1-28", dom)
	}
	h, m, err := parseTime(parts[1])
	if err != nil {
		return err
	}
	res.OnCalendar = fmt.Sprintf("*-*-%02d %02d:%02d:00", dom, h, m)
	res.Cron = fmt.Sprintf("%d %d %d * *", m, h, dom)
	return nil
}

func parseCalendar(spec, expr string, opts Options) (Result, error) {
	if strings.TrimSpace(expr) == "" {
		return Result{}, errors.New("oncalendar expression is empty")
	}
	for _, r := range expr {
		if r < 0x20 || r == 0x7f {
			return Result{}, errors.New("oncalendar expression contains control characters")
		}
	}
	expr = strings.TrimSpace(expr)
	if len(expr) > maxCalendar {
		return Result{}, fmt.Errorf("oncalendar expression longer than %d characters", maxCalendar)
	}
	if !calendarRe.MatchString(expr) {
		return Result{}, errors.New("oncalendar expression contains characters outside [A-Za-z0-9 *,.:/~-]")
	}
	if strings.HasPrefix(expr, "-") {
		return Result{}, errors.New("oncalendar expression must not start with '-'")
	}
	if opts.Analyze != nil {
		if err := opts.Analyze(expr); err != nil {
			return Result{}, fmt.Errorf("invalid oncalendar expression: %w", err)
		}
	}
	return Result{Spec: spec, Kind: "oncalendar", OnCalendar: expr}, nil
}

func parseTime(s string) (h, m int, err error) {
	sub := timeRe.FindStringSubmatch(s)
	if sub == nil {
		return 0, 0, fmt.Errorf("invalid time %q: want HH:MM between 00:00 and 23:59", s)
	}
	h, _ = strconv.Atoi(sub[1])
	m, _ = strconv.Atoi(sub[2])
	return h, m, nil
}
