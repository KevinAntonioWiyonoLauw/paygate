package qris

import (
	"strings"
	"testing"
)

// A real static QRIS payload shape (Gojek/QRIS national). The merchant account
// template and checksum are internally consistent so the parser must accept it.
const sampleStatic = "00020101021126610014COM.GO-JEK.WWW01189360091433610764120210G0336076410303UMI51440014ID.CO.QRIS.WWW0215ID10243251790120303UMI5204541153033605802ID5908Toko Umi6013Jakarta Pusat61051034062070703A0163047C87"

func TestValidChecksum(t *testing.T) {
	if !ValidChecksum(sampleStatic) {
		t.Fatalf("sample payload checksum rejected; recompute the fixture")
	}
	broken := sampleStatic[:len(sampleStatic)-4] + "0000"
	if ValidChecksum(broken) {
		t.Fatal("corrupted checksum accepted")
	}
}

func TestParseRoundTrip(t *testing.T) {
	elems, err := Parse(sampleStatic)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rebuilt := Finalize(Build(elems))
	if rebuilt != sampleStatic {
		t.Fatalf("round trip mismatch\n got: %s\nwant: %s", rebuilt, sampleStatic)
	}
}

func TestToDynamic(t *testing.T) {
	got, err := ToDynamic(sampleStatic, ConvertOptions{
		Amount:    50000,
		Reference: "ORD-20261026-4F2A9C",
	})
	if err != nil {
		t.Fatalf("to dynamic: %v", err)
	}
	if !ValidChecksum(got) {
		t.Fatal("output checksum invalid")
	}

	info := Describe(got)
	if !info.Valid {
		t.Fatal("describe reports invalid")
	}
	if !info.Dynamic {
		t.Fatal("point of initiation not flipped to dynamic")
	}
	if info.Amount != 50000 {
		t.Fatalf("amount = %d, want 50000", info.Amount)
	}
	if info.ReferenceLabel != "ORD-20261026-4F2A9C" {
		t.Fatalf("reference = %q", info.ReferenceLabel)
	}
	// Merchant identity must survive untouched.
	src := Describe(sampleStatic)
	if info.MerchantPAN != src.MerchantPAN || info.MerchantID != src.MerchantID {
		t.Fatalf("merchant identity changed: %+v -> %+v", src, info)
	}
}

func TestToDynamicRejectsBadChecksum(t *testing.T) {
	broken := sampleStatic[:len(sampleStatic)-4] + "FFFF"
	if _, err := ToDynamic(broken, ConvertOptions{Amount: 1000}); err == nil {
		t.Fatal("expected rejection of a payload with a bad checksum")
	}
}

func TestToDynamicRejectsZeroAmount(t *testing.T) {
	if _, err := ToDynamic(sampleStatic, ConvertOptions{Amount: 0}); err == nil {
		t.Fatal("expected rejection of zero amount")
	}
}

func TestReferenceTruncated(t *testing.T) {
	long := strings.Repeat("A", 40)
	got, err := ToDynamic(sampleStatic, ConvertOptions{Amount: 1000, Reference: long})
	if err != nil {
		t.Fatalf("to dynamic: %v", err)
	}
	if ref := Describe(got).ReferenceLabel; len(ref) != 25 {
		t.Fatalf("reference length = %d, want 25", len(ref))
	}
}

func TestRender(t *testing.T) {
	png, err := RenderPNG(sampleStatic, RenderOptions{Size: 256})
	if err != nil {
		t.Fatalf("render png: %v", err)
	}
	if len(png) < 100 || png[1] != 'P' || png[2] != 'N' || png[3] != 'G' {
		t.Fatalf("output is not a PNG (len=%d)", len(png))
	}
	svg, err := RenderSVG(sampleStatic, 6)
	if err != nil {
		t.Fatalf("render svg: %v", err)
	}
	if !strings.HasPrefix(string(svg), "<svg") {
		t.Fatalf("output is not SVG")
	}
}
