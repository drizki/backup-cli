package schedule

import (
	"errors"
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	cases := []struct {
		spec, cal, cron string
		gate            bool
	}{
		{"daily,02:30", "*-*-* 02:30:00", "30 2 * * *", false},
		{"daily,00:00", "*-*-* 00:00:00", "0 0 * * *", false},
		{"daily,23:59", "*-*-* 23:59:00", "59 23 * * *", false},
		{"weekly,mon,03:00", "Mon *-*-* 03:00:00", "0 3 * * 1", false},
		{"weekly,Sunday,04:15", "Sun *-*-* 04:15:00", "15 4 * * 0", false},
		{"weekly,FRI,22:05", "Fri *-*-* 22:05:00", "5 22 * * 5", false},
		{"weekly,tuesday,01:00", "Tue *-*-* 01:00:00", "0 1 * * 2", false},
		{"weekly,wed,01:00", "Wed *-*-* 01:00:00", "0 1 * * 3", false},
		{"weekly,thursday,01:00", "Thu *-*-* 01:00:00", "0 1 * * 4", false},
		{"weekly,saturday,01:00", "Sat *-*-* 01:00:00", "0 1 * * 6", false},
		{"biweekly,sat,05:00", "Sat *-*-* 05:00:00", "0 5 * * 6", true},
		{"monthly,1,06:00", "*-*-01 06:00:00", "0 6 1 * *", false},
		{"monthly,28,23:59", "*-*-28 23:59:00", "59 23 28 * *", false},
		{"  daily,01:01  ", "*-*-* 01:01:00", "1 1 * * *", false},
		{"oncalendar:Mon..Fri *-*-* 02:00:00", "Mon..Fri *-*-* 02:00:00", "", false},
		{"oncalendar:hourly", "hourly", "", false},
		{"OnCalendar:*-*-1,15 03:00", "*-*-1,15 03:00", "", false},
	}
	for _, c := range cases {
		r, err := Parse(c.spec, Options{})
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.spec, err)
			continue
		}
		if r.OnCalendar != c.cal || r.Cron != c.cron || r.BiweeklyGate != c.gate {
			t.Errorf("%q: got %+v", c.spec, r)
		}
		if r.Spec != strings.TrimSpace(c.spec) {
			t.Errorf("%q: spec not preserved: %q", c.spec, r.Spec)
		}
	}
}

func TestParseReject(t *testing.T) {
	cases := []struct{ spec, want string }{
		{"", "empty"},
		{"   ", "empty"},
		{"daily", "daily,HH:MM"},
		{"daily,", "invalid time"},
		{"daily,2:30", "invalid time"},
		{"daily,24:00", "invalid time"},
		{"daily,12:61", "invalid time"},
		{"daily,12:60", "invalid time"},
		{"daily,ab:cd", "invalid time"},
		{"daily,02:30,extra", "daily,HH:MM"},
		{"weekly,mon", "weekly,<day>"},
		{"weekly,funday,03:00", "unknown day"},
		{"weekly,mon,25:00", "invalid time"},
		{"biweekly,xyz,03:00", "unknown day"},
		{"biweekly,mon,03:00,x", "biweekly,<day>"},
		{"monthly,0,03:00", "out of range"},
		{"monthly,29,03:00", "1-28"},
		{"monthly,30,03:00", "1-28"},
		{"monthly,31,03:00", "1-28"},
		{"monthly,32,03:00", "out of range"},
		{"monthly,x,03:00", "invalid day of month"},
		{"monthly,1", "monthly,<day"},
		{"yearly,03:00", "unknown schedule"},
		{"oncalendar:", "empty"},
		{"oncalendar:   ", "empty"},
		{"oncalendar:daily\nExecStart=/bin/evil", "control"},
		{"oncalendar:daily\x00", "control"},
		{"oncalendar:daily;rm", "characters"},
		{"oncalendar:$(x)", "characters"},
		{"oncalendar:-h", "start with"},
		{"oncalendar:" + strings.Repeat("a", 201), "longer"},
	}
	for _, c := range cases {
		_, err := Parse(c.spec, Options{})
		if err == nil {
			t.Errorf("%q: expected error", c.spec)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %q does not contain %q", c.spec, err, c.want)
		}
	}
}

func TestAnalyzeRunner(t *testing.T) {
	var got string
	ok := Options{Analyze: func(e string) error { got = e; return nil }}
	if _, err := Parse("oncalendar:weekly", ok); err != nil || got != "weekly" {
		t.Fatalf("analyze not called: %v %q", err, got)
	}
	bad := Options{Analyze: func(string) error { return errors.New("nope") }}
	if _, err := Parse("oncalendar:weekly", bad); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected analyzer error, got %v", err)
	}
	// Analyze must not be consulted for non-oncalendar specs.
	called := false
	if _, err := Parse("daily,01:00", Options{Analyze: func(string) error { called = true; return nil }}); err != nil || called {
		t.Fatal("analyze called for daily")
	}
}
