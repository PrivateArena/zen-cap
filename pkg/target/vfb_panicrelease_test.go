package target

import "testing"

// The VFB backend wraps an X11Target, so it can honour the optional
// PanicReleaser capability without that capability polluting the Target
// interface (and thus adb/vnc/wda). These are compile-time assertions; the
// runtime behaviour is exercised by TestLiveReleaseAllButtonsRecoversStuckPress.
var (
	_ PanicReleaser = (*VFBTarget)(nil)
	_ PanicReleaser = (*X11Target)(nil)

	// Backends that deliberately do NOT implement PanicReleaser stay as they
	// were; if one ever starts to, this is the place that will complain.
	_ Target = (*VFBTarget)(nil)
	_ Target = (*X11Target)(nil)
)

func TestVFBTargetIsPanicReleaser(t *testing.T) {
	var tgt Target = &VFBTarget{}
	pr, ok := tgt.(PanicReleaser)
	if !ok {
		t.Fatal("VFBTarget must satisfy target.PanicReleaser")
	}
	// No Xvfb attached: nothing to release, and that must not be an error.
	if err := pr.ReleaseAllButtons(); err != nil {
		t.Errorf("ReleaseAllButtons on an unattached VFB target: %v", err)
	}
}

func TestNonX11BackendsAreNotPanicReleasers(t *testing.T) {
	// The API plan type-asserts at the call site, so a backend that cannot
	// express a release-only primitive must not pretend it can.
	for name, tgt := range map[string]Target{
		"adb": (*ADBTarget)(nil),
		"wda": (*WDATarget)(nil),
		"vnc": (*VNCTarget)(nil),
	} {
		if _, ok := tgt.(interface{ ReleaseAllButtons() error }); ok {
			t.Errorf("%s target unexpectedly implements PanicReleaser", name)
		}
	}
}
