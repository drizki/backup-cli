package exitcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestCodeMapping(t *testing.T) {
	plain := errors.New("boom")
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, OK},
		{"plain", plain, Failed},
		{"usage", Usagef("bad %d", 1), Usage},
		{"refused", Refusedf("no"), Refused},
		{"failed", Failedf("io"), Failed},
		{"partial", With(Partial, plain), Partial},
		{"wrapped", fmt.Errorf("ctx: %w", Refusedf("x")), Refused},
		{"with nil", With(Usage, nil), OK},
	}
	for _, c := range cases {
		if got := Code(c.err); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestConstantsAndNames(t *testing.T) {
	want := map[int]string{0: "ok", 1: "failed", 2: "usage", 3: "partial", 4: "refused", 9: "unknown"}
	for c, n := range want {
		if Name(c) != n {
			t.Errorf("Name(%d)=%q want %q", c, Name(c), n)
		}
	}
	if OK != 0 || Failed != 1 || Usage != 2 || Partial != 3 || Refused != 4 {
		t.Fatal("exit code constants changed")
	}
}

func TestErrorUnwrap(t *testing.T) {
	base := errors.New("base")
	e := With(Failed, base)
	if !errors.Is(e, base) || e.Error() != "base" {
		t.Fatal("unwrap broken")
	}
}
