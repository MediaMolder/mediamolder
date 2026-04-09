package av

import "testing"

// RequireHWDevice skips the test if the given hardware device type is not available.
// This allows hardware tests to run in GPU-equipped CI runners and gracefully
// skip on systems without the required hardware.
func RequireHWDevice(t *testing.T, deviceType HWDeviceType) *HWDeviceContext {
	t.Helper()
	dev, err := OpenHWDevice(deviceType, "")
	if err != nil {
		t.Skipf("hardware device %s not available: %v", deviceType, err)
		return nil
	}
	t.Cleanup(func() { dev.Close() })
	return dev
}

// RequireAnyHWDevice skips the test if no hardware device types are available.
// Returns the first available device context and its type.
func RequireAnyHWDevice(t *testing.T) (*HWDeviceContext, HWDeviceType) {
	t.Helper()
	for _, dt := range ListHWDeviceTypes() {
		dev, err := OpenHWDevice(dt, "")
		if err == nil {
			t.Cleanup(func() { dev.Close() })
			return dev, dt
		}
	}
	t.Skip("no hardware acceleration devices available")
	return nil, HWDeviceNone
}
