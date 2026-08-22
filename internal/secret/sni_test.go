package secret

import "testing"

func TestDeriveSNIKnownVector(t *testing.T) {
	ca := "-----BEGIN CERTIFICATE-----\nYWJj\n-----END CERTIFICATE-----"
	jwt := "-----BEGIN PUBLIC KEY-----\nZGVm\n-----END PUBLIC KEY-----"

	got, err := DeriveSNI(ca, jwt)
	if err != nil {
		t.Fatal(err)
	}
	const want = "c08a4a89981573a561cd041a620ebcce.5ce2e41a9e.net"
	if got != want {
		t.Fatalf("DeriveSNI() = %q, want %q", got, want)
	}
}

func TestDeriveSNIIgnoresPEMFormatting(t *testing.T) {
	compact, err := DeriveSNI("-----BEGIN CERTIFICATE-----YWJj-----END CERTIFICATE-----", "ZGVm")
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := DeriveSNI("-----BEGIN CERTIFICATE-----\r\nYW Jj\r\n-----END CERTIFICATE-----", "-----BEGIN PUBLIC KEY-----\nZGVm\n-----END PUBLIC KEY-----")
	if err != nil {
		t.Fatal(err)
	}
	if compact != formatted {
		t.Fatalf("formatting changed SNI: %q != %q", compact, formatted)
	}
}

func TestDeriveSNIRejectsEmptyMaterial(t *testing.T) {
	if _, err := DeriveSNI("-----BEGIN CERTIFICATE-----", "-----END CERTIFICATE-----"); err == nil {
		t.Fatal("expected empty material to fail")
	}
}
