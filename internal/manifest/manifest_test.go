package manifest

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	signed := &SigningMetadata{KeyID: "prod-2026", Algorithm: AlgorithmEd25519, Signature: "c2ln"}
	base := Manifest{Framework: FrameworkESPIDF, VersionLabel: "1.0.0", ChipFamily: "esp32", SecurityMode: SecurityBasic}
	tests := []struct {
		name    string
		mutate  func(*Manifest)
		wantErr string
	}{
		{name: "basic ok", mutate: func(*Manifest) {}},
		{name: "arduino ok", mutate: func(m *Manifest) { m.Framework = FrameworkArduino }},
		{name: "signed ok", mutate: func(m *Manifest) { m.SecurityMode = SecuritySigned; m.SigningMetadata = signed }},
		{name: "bad framework", mutate: func(m *Manifest) { m.Framework = "esp-idf" }, wantErr: "framework"},
		{name: "empty label", mutate: func(m *Manifest) { m.VersionLabel = "" }, wantErr: "version label"},
		{name: "padded label", mutate: func(m *Manifest) { m.VersionLabel = " 1.0" }, wantErr: "version label"},
		{name: "long label", mutate: func(m *Manifest) { m.VersionLabel = strings.Repeat("a", 65) }, wantErr: "64"},
		{name: "control char", mutate: func(m *Manifest) { m.VersionLabel = "1.0\n" }, wantErr: "version label"},
		{name: "bad chip", mutate: func(m *Manifest) { m.ChipFamily = "ESP 32" }, wantErr: "chip"},
		{name: "sha256 ok", mutate: func(m *Manifest) { m.SHA256 = strings.Repeat("ab", 32) }},
		{name: "short sha256", mutate: func(m *Manifest) { m.SHA256 = "ab12" }, wantErr: "sha256"},
		{name: "upper-case sha256", mutate: func(m *Manifest) { m.SHA256 = strings.Repeat("AB", 32) }, wantErr: "sha256"},
		{name: "token mode", mutate: func(m *Manifest) { m.SecurityMode = "token" }, wantErr: "security mode"},
		{name: "basic with signature", mutate: func(m *Manifest) { m.SigningMetadata = signed }, wantErr: "basic"},
		{name: "signed without metadata", mutate: func(m *Manifest) { m.SecurityMode = SecuritySigned }, wantErr: "signed"},
		{name: "signed bad key id", mutate: func(m *Manifest) {
			m.SecurityMode = SecuritySigned
			m.SigningMetadata = &SigningMetadata{KeyID: "-bad", Algorithm: AlgorithmEd25519, Signature: "x"}
		}, wantErr: "signed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base
			tt.mutate(&m)
			err := m.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestNormalizeChip(t *testing.T) {
	for in, want := range map[string]string{"esp32": "esp32", " ESP32S3 ": "esp32s3", "esp32-c3": "esp32-c3"} {
		got, err := NormalizeChip(in)
		if err != nil || got != want {
			t.Errorf("NormalizeChip(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "esp 32", "esp32;rm", strings.Repeat("a", 33)} {
		if _, err := NormalizeChip(bad); err == nil {
			t.Errorf("NormalizeChip(%q) should fail", bad)
		}
	}
}

func TestValidKeyID(t *testing.T) {
	for id, want := range map[string]bool{
		"prod-2026": true, "a": true, "key.v2_x": true,
		"": false, "-x": false, "has space": false, strings.Repeat("a", 65): false,
	} {
		if got := ValidKeyID(id); got != want {
			t.Errorf("ValidKeyID(%q) = %v, want %v", id, got, want)
		}
	}
}
