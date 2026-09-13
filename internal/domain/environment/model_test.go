package environment

import "testing"

func TestEnvironmentValidation(t *testing.T) {
	tests := []struct {
		name    string
		value   Environment
		wantErr bool
	}{
		{name: "source kubernetes", value: Environment{Name: "source", Role: RoleSource, Kind: KindKubernetes}},
		{name: "source compose", value: Environment{Name: "compose", Role: RoleSource, Kind: KindDockerCompose}},
		{name: "target kubernetes", value: Environment{Name: "sks", Role: RoleTarget, Kind: KindKubernetes}},
		{name: "compose target rejected", value: Environment{Name: "bad", Role: RoleTarget, Kind: KindDockerCompose}, wantErr: true},
		{name: "empty name rejected", value: Environment{Role: RoleSource, Kind: KindKubernetes}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.value.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestSnapshotDriverMatchesStorageClassProvisioner(t *testing.T) {
	capabilities := Capabilities{
		StorageClasses:             []StorageClass{{Name: "block", Provisioner: "csi.block.example"}, {Name: "default", Provisioner: "csi.default.example", Default: true}},
		VolumeSnapshotClassDetails: []VolumeSnapshotClass{{Name: "block-snapshot", Driver: "csi.block.example"}, {Name: "default-snapshot", Driver: "csi.default.example"}},
	}
	if driver, found := capabilities.SnapshotDriverForStorageClass("block"); !found || driver != "csi.block.example" {
		t.Fatalf("explicit StorageClass match = %q, %v", driver, found)
	}
	if driver, found := capabilities.SnapshotDriverForStorageClass(""); !found || driver != "csi.default.example" {
		t.Fatalf("default StorageClass match = %q, %v", driver, found)
	}
	capabilities.VolumeSnapshotClassDetails = capabilities.VolumeSnapshotClassDetails[:1]
	if _, found := capabilities.SnapshotDriverForStorageClass(""); found {
		t.Fatal("a different snapshot driver must not be accepted")
	}
}

func TestRawBlockDataMoverRequiresLinuxOnlyNodes(t *testing.T) {
	if !(Capabilities{OperatingSystems: []string{"linux"}}).SupportsRawBlockDataMover() {
		t.Fatal("Linux-only cluster should support Raw Block Data Mover")
	}
	for _, values := range [][]string{nil, {"windows"}, {"linux", "windows"}} {
		if (Capabilities{OperatingSystems: values}).SupportsRawBlockDataMover() {
			t.Fatalf("operating systems %v must not be accepted for Raw Block", values)
		}
	}
}
