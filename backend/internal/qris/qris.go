// Package qris implements EMVCo merchant-presented QR parsing, dynamic payload
// construction, and PNG rendering. The wire format is public (EMVCo MPM), so
// nothing here depends on any provider SDK.
package qris

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Well-known tags.
const (
	TagPayloadFormat      = "00"
	TagPointOfInitiation  = "01"
	TagMerchantAccount26  = "26" // .. 51 are merchant account templates
	TagMerchantAccount51  = "51"
	TagMerchantCategory   = "52"
	TagCurrency           = "53"
	TagAmount             = "54"
	TagTipIndicator       = "55"
	TagFeeFixed           = "56"
	TagFeePercent         = "57"
	TagCountry            = "58"
	TagMerchantName       = "59"
	TagMerchantCity       = "60"
	TagPostalCode         = "61"
	TagAdditionalData     = "62"
	TagCRC                = "63"
	TagReferenceLabel     = "05" // sub-tag of 62
	TagBillNumber         = "01" // sub-tag of 62
	TagStoreLabel         = "03" // sub-tag of 62
	TagTerminalLabel      = "07" // sub-tag of 62
	PointOfInitiationStatic  = "11"
	PointOfInitiationDynamic = "12"
	CurrencyIDR           = "360"
)

// Element is one TLV node. Nested templates (merchant account, additional data)
// keep their children so a round-trip preserves structure exactly.
type Element struct {
	Tag      string
	Value    string
	Children []Element
}

// ErrNotQRIS is returned when the payload does not look like EMVCo MPM.
var ErrNotQRIS = errors.New("payload is not a valid QRIS string")

// Parse decodes a QRIS payload into ordered TLV elements, recursing into the
// known template tags. A malformed length or a truncated value is an error, not
// a silent truncation.
func Parse(payload string) ([]Element, error) {
	payload = strings.TrimSpace(payload)
	if len(payload) < 8 {
		return nil, ErrNotQRIS
	}
	// Cheap sanity check before walking: it must at least start with tag 00.
	if !strings.HasPrefix(payload, TagPayloadFormat) {
		return nil, ErrNotQRIS
	}
	elems, consumed, err := parseTLV(payload)
	if err != nil {
		return nil, err
	}
	if consumed != len(payload) {
		return nil, fmt.Errorf("%w: trailing bytes at offset %d", ErrNotQRIS, consumed)
	}
	return elems, nil
}

func parseTLV(s string) ([]Element, int, error) {
	var out []Element
	i := 0
	for i < len(s) {
		if i+4 > len(s) {
			return nil, i, fmt.Errorf("%w: truncated header at offset %d", ErrNotQRIS, i)
		}
		tag := s[i : i+2]
		if !isDigits(tag) {
			return nil, i, fmt.Errorf("%w: non-numeric tag %q at offset %d", ErrNotQRIS, tag, i)
		}
		length, err := strconv.Atoi(s[i+2 : i+4])
		if err != nil {
			return nil, i, fmt.Errorf("%w: bad length at offset %d", ErrNotQRIS, i)
		}
		start := i + 4
		end := start + length
		if end > len(s) {
			return nil, i, fmt.Errorf("%w: value for tag %s runs past end", ErrNotQRIS, tag)
		}
		value := s[start:end]

		elem := Element{Tag: tag, Value: value}
		if isTemplate(tag) && length > 0 {
			children, childConsumed, childErr := parseTLV(value)
			if childErr == nil && childConsumed == len(value) {
				elem.Children = children
			}
			// A template we cannot decode stays opaque rather than failing the
			// whole payload: some issuers put non-TLV data in 62/80.
		}
		out = append(out, elem)
		i = end
	}
	return out, i, nil
}

func isTemplate(tag string) bool {
	if tag == TagAdditionalData {
		return true
	}
	n, err := strconv.Atoi(tag)
	if err != nil {
		return false
	}
	return n >= 26 && n <= 51
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Build serialises elements back to a wire string, in ascending tag order.
// EMVCo expects ordering; strict validators reject out-of-order payloads.
// Tag 63 (CRC) is skipped: callers append it with Finalize, which recomputes it.
func Build(elems []Element) string {
	ordered := make([]Element, len(elems))
	copy(ordered, elems)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Tag < ordered[j].Tag })

	var b strings.Builder
	for _, e := range ordered {
		if e.Tag == TagCRC {
			continue
		}
		value := e.Value
		if len(e.Children) > 0 {
			value = Build(e.Children)
		}
		b.WriteString(e.Tag)
		fmt.Fprintf(&b, "%02d", len(value))
		b.WriteString(value)
	}
	return b.String()
}

// CRC16CCITTFalse computes the checksum EMVCo specifies for tag 63.
func CRC16CCITTFalse(data []byte) uint16 {
	var crc uint16 = 0xFFFF
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// ValidChecksum reports whether the trailing tag 63 matches the payload.
func ValidChecksum(payload string) bool {
	payload = strings.TrimSpace(payload)
	if len(payload) < 8 {
		return false
	}
	body := payload[:len(payload)-4]
	given := payload[len(payload)-4:]
	want := fmt.Sprintf("%04X", CRC16CCITTFalse([]byte(body)))
	return strings.EqualFold(given, want)
}

// Finalize appends tag 63 with a freshly computed CRC. Input must not already
// contain tag 63.
func Finalize(body string) string {
	withTag := body + TagCRC + "04"
	return withTag + fmt.Sprintf("%04X", CRC16CCITTFalse([]byte(withTag)))
}

// ConvertOptions controls the static -> dynamic conversion.
type ConvertOptions struct {
	// Amount in whole rupiah. Required, must be positive.
	Amount int64
	// Reference is written to tag 62/05 (Reference Label, max 25 chars).
	// Optional: when the provider ignores it, matching falls back to amount.
	Reference string
	// BillNumber is written to tag 62/01. Optional.
	BillNumber string
	// StoreLabel is written to tag 62/03. Optional.
	StoreLabel string
}

// ToDynamic turns a static QRIS payload into a dynamic one carrying a fixed
// amount and an optional reference, then recomputes the CRC.
//
// The merchant account templates (26..51) are copied verbatim. Editing them
// would change who gets paid, so they are never touched.
func ToDynamic(staticPayload string, opts ConvertOptions) (string, error) {
	if opts.Amount <= 0 {
		return "", fmt.Errorf("qris: amount must be positive, got %d", opts.Amount)
	}
	if !ValidChecksum(staticPayload) {
		return "", fmt.Errorf("%w: source checksum is invalid, refusing to build a payable QR", ErrNotQRIS)
	}
	elems, err := Parse(staticPayload)
	if err != nil {
		return "", err
	}

	reference := truncate(opts.Reference, 25)
	bill := truncate(opts.BillNumber, 25)
	storeLabel := truncate(opts.StoreLabel, 25)
	needsAdditional := reference != "" || bill != "" || storeLabel != ""

	var rebuilt []Element
	seen := map[string]bool{}
	amountSet := false

	for _, e := range elems {
		switch e.Tag {
		case TagCRC:
			continue // recomputed at the end
		case TagAmount:
			rebuilt = append(rebuilt, Element{Tag: TagAmount, Value: strconv.FormatInt(opts.Amount, 10)})
			seen[TagAmount] = true
			amountSet = true
			continue
		case TagPointOfInitiation:
			rebuilt = append(rebuilt, Element{Tag: TagPointOfInitiation, Value: PointOfInitiationDynamic})
			seen[TagPointOfInitiation] = true
			continue
		case TagAdditionalData:
			if !needsAdditional {
				rebuilt = append(rebuilt, e)
				continue
			}
			merged := mergeAdditional(e.Children, bill, reference, storeLabel)
			rebuilt = append(rebuilt, Element{Tag: TagAdditionalData, Children: merged})
			seen[TagAdditionalData] = true
			continue
		}
		rebuilt = append(rebuilt, e)
		seen[e.Tag] = true
	}

	if !amountSet {
		rebuilt = append(rebuilt, Element{Tag: TagAmount, Value: strconv.FormatInt(opts.Amount, 10)})
	}
	if !seen[TagPointOfInitiation] {
		rebuilt = append(rebuilt, Element{Tag: TagPointOfInitiation, Value: PointOfInitiationDynamic})
	}
	if needsAdditional && !seen[TagAdditionalData] {
		rebuilt = append(rebuilt, Element{
			Tag:      TagAdditionalData,
			Children: mergeAdditional(nil, bill, reference, storeLabel),
		})
	}

	return Finalize(Build(rebuilt)), nil
}

// mergeAdditional sets 62/01, 62/03, 62/05 while preserving every other sub-tag.
func mergeAdditional(existing []Element, bill, reference, storeLabel string) []Element {
	byTag := make(map[string]Element, len(existing))
	order := make([]string, 0, len(existing))
	for _, e := range existing {
		if _, ok := byTag[e.Tag]; !ok {
			order = append(order, e.Tag)
		}
		byTag[e.Tag] = e
	}
	set := func(tag, value string) {
		if value == "" {
			return
		}
		if _, ok := byTag[tag]; !ok {
			order = append(order, tag)
		}
		byTag[tag] = Element{Tag: tag, Value: value}
	}
	set(TagBillNumber, bill)
	set(TagStoreLabel, storeLabel)
	set(TagReferenceLabel, reference)

	out := make([]Element, 0, len(order))
	for _, tag := range order {
		out = append(out, byTag[tag])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// Inspect returns the human-relevant fields of a payload, for the UI.
type Inspect struct {
	Valid          bool   `json:"valid"`
	Dynamic        bool   `json:"dynamic"`
	Amount         int64  `json:"amount,omitempty"`
	Currency       string `json:"currency,omitempty"`
	Country        string `json:"country,omitempty"`
	MerchantName   string `json:"merchant_name,omitempty"`
	MerchantCity   string `json:"merchant_city,omitempty"`
	PostalCode     string `json:"postal_code,omitempty"`
	MCC            string `json:"mcc,omitempty"`
	ReferenceLabel string `json:"reference_label,omitempty"`
	BillNumber     string `json:"bill_number,omitempty"`
	StoreLabel     string `json:"store_label,omitempty"`
	MerchantGUI    string `json:"merchant_gui,omitempty"`
	MerchantPAN    string `json:"merchant_pan,omitempty"`
	MerchantID     string `json:"merchant_id,omitempty"`
	Acquirer       string `json:"acquirer,omitempty"`
}

// Describe parses a payload and returns a summary. Never returns an error: an
// invalid payload yields Valid=false plus whatever could be recovered.
func Describe(payload string) Inspect {
	out := Inspect{Valid: ValidChecksum(payload)}
	elems, err := Parse(payload)
	if err != nil {
		return out
	}
	for _, e := range elems {
		switch e.Tag {
		case TagPointOfInitiation:
			out.Dynamic = e.Value == PointOfInitiationDynamic
		case TagAmount:
			if n, convErr := strconv.ParseInt(e.Value, 10, 64); convErr == nil {
				out.Amount = n
			}
		case TagCurrency:
			out.Currency = e.Value
		case TagCountry:
			out.Country = e.Value
		case TagMerchantName:
			out.MerchantName = e.Value
		case TagMerchantCity:
			out.MerchantCity = e.Value
		case TagPostalCode:
			out.PostalCode = e.Value
		case TagMerchantCategory:
			out.MCC = e.Value
		case TagAdditionalData:
			for _, child := range e.Children {
				switch child.Tag {
				case TagReferenceLabel:
					out.ReferenceLabel = child.Value
				case TagBillNumber:
					out.BillNumber = child.Value
				case TagStoreLabel:
					out.StoreLabel = child.Value
				}
			}
		}
		if n, convErr := strconv.Atoi(e.Tag); convErr == nil && n >= 26 && n <= 51 && out.Acquirer == "" {
			for _, child := range e.Children {
				switch child.Tag {
				case "00":
					out.MerchantGUI = child.Value
				case "01":
					out.MerchantPAN = child.Value
				case "02":
					out.MerchantID = child.Value
				}
			}
			if out.MerchantGUI != "" {
				out.Acquirer = out.MerchantGUI
			}
		}
	}
	return out
}
