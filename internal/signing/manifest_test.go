package signing

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
)

const vectorSHA = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

// The shared test vector: the firmware client's host test and the server
// check the same bytes and signature.
func TestManifestMatchesSharedVector(t *testing.T) {
	seed, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	priv := ed25519.NewKeyFromSeed(seed)
	if got := hex.EncodeToString(priv.Public().(ed25519.PublicKey)); got != "03a107bff3ce10be1d70dd18e74bc09967e4d6309ba50d5f1ddc8664125531b8" {
		t.Fatalf("public key = %s", got)
	}
	m := Manifest{ProjectID: "proj_7f3a9c", KeyID: "prod-2026", Build: 42, SHA256: strings.ToUpper(vectorSHA)}

	msg, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	want := "SimpleOTA-Manifest-v1\nproj_7f3a9c\nprod-2026\n42\n" + vectorSHA + "\n"
	if string(msg) != want || len(msg) != 112 {
		t.Fatalf("manifest = %q (%d bytes)", msg, len(msg))
	}
	sig, err := SignManifest(priv, m)
	if err != nil {
		t.Fatal(err)
	}
	if sig != "bF9i2/VJN7LK85vlh5XNU+ouiQFhxFQ79dW5Y+NNek1K96GD8K+dKjjD3AGJyu1hx7q+eR9LaJQOOKJXI+PRAw==" {
		t.Fatalf("signature = %s", sig)
	}
}

func TestManifestRefusesFieldsDevicesCannotRebuild(t *testing.T) {
	ok := Manifest{ProjectID: "p", KeyID: "k", Build: 1, SHA256: vectorSHA}
	for name, m := range map[string]Manifest{
		"no project":         {KeyID: "k", Build: 1, SHA256: vectorSHA},
		"newline in project": {ProjectID: "p\nq", KeyID: "k", Build: 1, SHA256: vectorSHA},
		"newline in key":     {ProjectID: "p", KeyID: "k\n", Build: 1, SHA256: vectorSHA},
		"build 0":            {ProjectID: "p", KeyID: "k", SHA256: vectorSHA},
		"short sha":          {ProjectID: "p", KeyID: "k", Build: 1, SHA256: vectorSHA[:63]},
		"non-hex sha":        {ProjectID: "p", KeyID: "k", Build: 1, SHA256: "g" + vectorSHA[1:]},
	} {
		if _, err := m.Bytes(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := ok.Bytes(); err != nil {
		t.Fatal(err)
	}
	// A build with no key ID has an empty key line.
	ok.KeyID = ""
	if msg, _ := ok.Bytes(); !strings.Contains(string(msg), "\np\n\n1\n") {
		t.Fatalf("manifest = %q", msg)
	}
}
