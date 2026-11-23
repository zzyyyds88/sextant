package perms

import "testing"

func TestRuntimeIsAKnownGroup(t *testing.T) {
	// An unknown group is rejected by config validation, so a typo in the config
	// would make the whole permission pass a no-op without saying so.
	if !Valid(Runtime) {
		t.Fatal("runtime must be a known group")
	}
	for _, g := range []string{Idle, Overlay, Usage, Notif, Alarm, UnknownApps, BgPopup, Runtime} {
		if !Valid(g) {
			t.Fatalf("%q is not known", g)
		}
	}
}

func TestRuntimePermissionsAreTheOnesTheAppDeclares(t *testing.T) {
	// Derived lists go stale silently: the group reports OK while the permission it
	// should have granted is not in the list any more. This pins the content.
	if len(runtimePermissions) == 0 {
		t.Fatal("runtime group has no permissions")
	}
	found := false
	for _, p := range runtimePermissions {
		if p == "android.permission.POST_NOTIFICATIONS" {
			found = true
		}
	}
	if !found {
		t.Fatalf("POST_NOTIFICATIONS missing from %v", runtimePermissions)
	}
}

// CAPTURE_AUDIO_OUTPUT must NOT be here: `pm grant` cannot grant it (signature|
// privileged), so listing it would make the group report MISSING forever and teach
// the reader to ignore the group.
func TestPrivilegedPermissionsAreNotRuntimeGrants(t *testing.T) {
	for _, p := range runtimePermissions {
		switch p {
		case "android.permission.CAPTURE_AUDIO_OUTPUT",
			"android.permission.CAPTURE_MEDIA_OUTPUT",
			"android.permission.MODIFY_PHONE_STATE":
			t.Fatalf("%s cannot be granted by pm grant; it belongs in the priv-app whitelist", p)
		}
	}
}
