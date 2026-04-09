package av

import "testing"

func TestCheckVersion(t *testing.T) {
	if err := CheckVersion(); err != nil {
		t.Errorf("CheckVersion() = %v; want nil (FFmpeg 8.1+ required)", err)
	}
}

func TestLibVersions(t *testing.T) {
	s := LibVersions()
	if s == "" {
		t.Error("LibVersions() returned empty string")
	}
	t.Log("linked libraries:", s)
}
