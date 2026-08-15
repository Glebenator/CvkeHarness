package securitypolicy

import "testing"

func TestEveryBundleResolvesEverySetting(t *testing.T) {
	for _, bundle := range Profiles() {
		resolved, err := Resolve(&Selection{Version: SchemaVersion, Profile: bundle.ID})
		if err != nil {
			t.Fatalf("Resolve(%s): %v", bundle.ID, err)
		}
		for _, setting := range Catalog() {
			value := resolved.Value(setting.ID)
			if value == "" {
				t.Fatalf("profile %s omitted %s", bundle.ID, setting.ID)
			}
			if err := validateValue(setting, value); err != nil {
				t.Fatalf("profile %s has invalid %s=%q: %v", bundle.ID, setting.ID, value, err)
			}
		}
	}
}

func TestReasonableIsDefaultAndOverridesAreIsolated(t *testing.T) {
	selection := DefaultSelection()
	if selection.Profile != ProfileReasonable {
		t.Fatalf("default profile = %q", selection.Profile)
	}
	base, err := Resolve(selection)
	if err != nil {
		t.Fatal(err)
	}
	if got := base.Decision(SettingFileDelete); got != DecisionAsk {
		t.Fatalf("reasonable delete = %q", got)
	}
	if err := selection.SetOverride(SettingFileDelete, string(DecisionDeny)); err != nil {
		t.Fatal(err)
	}
	custom, err := Resolve(selection)
	if err != nil {
		t.Fatal(err)
	}
	if got := custom.Decision(SettingFileDelete); got != DecisionDeny {
		t.Fatalf("overridden delete = %q", got)
	}
	if got := custom.Decision(SettingFileOverwrite); got != base.Decision(SettingFileOverwrite) {
		t.Fatalf("unrelated overwrite changed from %q to %q", base.Decision(SettingFileOverwrite), got)
	}
	if custom.Origins[SettingFileDelete] != "override" || custom.OverrideCount() != 1 {
		t.Fatalf("unexpected origins/count: %#v %d", custom.Origins, custom.OverrideCount())
	}
}

func TestInvalidSelectionFailsClosed(t *testing.T) {
	cases := []*Selection{
		{Version: SchemaVersion, Profile: "mystery"},
		{Version: SchemaVersion, Profile: ProfileReasonable, Overrides: map[string]string{"unknown.setting": "allow"}},
		{Version: SchemaVersion, Profile: ProfileReasonable, Overrides: map[string]string{SettingFileDelete: "maybe"}},
		{Version: SchemaVersion + 1, Profile: ProfileReasonable},
	}
	for _, selection := range cases {
		if _, err := Resolve(selection); err == nil {
			t.Fatalf("expected invalid selection %#v to fail", selection)
		}
	}
}

func TestProfileRiskOrderingForDestructiveEffects(t *testing.T) {
	want := map[Profile]Decision{
		ProfileExtraStrict: DecisionDeny,
		ProfileReasonable:  DecisionAsk,
		ProfileLessStrict:  DecisionAsk,
		ProfileMinimal:     DecisionAllow,
		ProfileYOLO:        DecisionAllow,
	}
	for profile, expected := range want {
		resolved, err := Resolve(&Selection{Version: SchemaVersion, Profile: profile})
		if err != nil {
			t.Fatal(err)
		}
		if got := resolved.Decision(SettingFileDelete); got != expected {
			t.Fatalf("%s delete = %q, want %q", profile, got, expected)
		}
	}
}

func TestApplyProfileClearsOverrides(t *testing.T) {
	selection := DefaultSelection()
	_ = selection.SetOverride(SettingNetworkAccess, string(DecisionDeny))
	if err := selection.ApplyProfile(ProfileMinimal); err != nil {
		t.Fatal(err)
	}
	if selection.Profile != ProfileMinimal || len(selection.Overrides) != 0 {
		t.Fatalf("unexpected selection after apply: %#v", selection)
	}
}
