package logx

import (
	"bytes"
	"testing"
)

func TestLevels(t *testing.T) {
	var b bytes.Buffer
	l := New(&b, false)
	l.Infof("a %d", 1)
	l.Debugf("hidden")
	if b.String() != "a 1\n" || l.Verbose() {
		t.Fatalf("got %q", b.String())
	}
	b.Reset()
	l = New(&b, true)
	l.Debugf("shown")
	if b.String() != "debug: shown\n" || !l.Verbose() {
		t.Fatalf("got %q", b.String())
	}
	New(nil, true).Infof("discard")
}
