package schedule

import (
	"strings"
	"testing"
)

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"daily,02:30", "weekly,mon,01:00", "biweekly,Sunday,23:59", "monthly,28,00:00",
		"oncalendar:*-*-* 02:00", "oncalendar:\n", "daily,24:00", "monthly,31,00:00", "", ",,,",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := Parse(s, Options{})
		if err != nil {
			return
		}
		for _, v := range []string{r.OnCalendar, r.Cron} {
			if strings.ContainsAny(v, "\n\r\x00") {
				t.Fatalf("%q produced control characters: %q", s, v)
			}
		}
		if r.OnCalendar == "" {
			t.Fatalf("%q: empty OnCalendar", s)
		}
	})
}
