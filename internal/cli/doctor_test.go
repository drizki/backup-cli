package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/drizki/backup-cli/internal/doctor"
	"github.com/drizki/backup-cli/internal/exitcode"
)

func TestDoctorHumanAndJSON(t *testing.T) {
	e := newRmEnv(t)
	e.mustRun("doctor")
	for _, want := range []string{"state directory", "source readable [one]", "backups [one]: 2 committed", "check(s)"} {
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, e.out.String())
		}
	}
	e.mustRun("doctor", "--json")
	var rep doctor.Report
	if err := json.Unmarshal(e.out.Bytes(), &rep); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if !rep.OK || rep.Failures != 0 || len(rep.Checks) == 0 || rep.Version == "" {
		t.Fatalf("%+v", rep)
	}
}

func TestDoctorWarningsStillExitZero(t *testing.T) {
	e := newRmEnv(t)
	e.fake.Avail = false
	e.mustRun("doctor", "--json")
	var rep doctor.Report
	if err := json.Unmarshal(e.out.Bytes(), &rep); err != nil || rep.Warnings == 0 || rep.Failures != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
}

func TestDoctorFailureExitsOne(t *testing.T) {
	e := newRmEnv(t)
	if err := os.RemoveAll(e.src); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Failed, "doctor")
	if !strings.Contains(e.out.String(), "[FAIL] source readable") {
		t.Fatal(e.out.String())
	}
	e.expectCode(exitcode.Failed, "doctor", "--json")
	var rep doctor.Report
	if err := json.Unmarshal(e.out.Bytes(), &rep); err != nil || rep.OK || rep.Failures == 0 {
		t.Fatalf("%v %+v", err, rep)
	}
}
