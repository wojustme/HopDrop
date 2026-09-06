package protocol

import "testing"

func TestValidateOffer(t *testing.T) {
	valid := Offer{
		Files:      []FileMeta{{ID: "1", Name: "photo.jpg", RelPath: "album/photo.jpg", Size: 12}},
		TotalBytes: 12,
	}
	if _, err := ValidateOffer(valid); err != nil {
		t.Fatalf("valid offer rejected: %v", err)
	}

	cases := map[string]Offer{
		"traversal":      {Files: []FileMeta{{ID: "1", Name: "x", RelPath: "../x", Size: 1}}, TotalBytes: 1},
		"backslash":      {Files: []FileMeta{{ID: "1", Name: "x", RelPath: `dir\x`, Size: 1}}, TotalBytes: 1},
		"duplicate id":   {Files: []FileMeta{{ID: "1", Name: "a", RelPath: "a"}, {ID: "1", Name: "b", RelPath: "b"}}},
		"name mismatch":  {Files: []FileMeta{{ID: "1", Name: "a", RelPath: "dir/b"}}},
		"total mismatch": {Files: []FileMeta{{ID: "1", Name: "a", RelPath: "a", Size: 2}}, TotalBytes: 1},
	}
	for name, offer := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateOffer(offer); err == nil {
				t.Fatal("invalid offer accepted")
			}
		})
	}
}

func TestAcceptedFiles(t *testing.T) {
	offer := Offer{Files: []FileMeta{
		{ID: "a", Name: "a", RelPath: "a"},
		{ID: "b", Name: "b", RelPath: "b"},
	}}
	all, err := AcceptedFiles(offer, Decision{Accept: true})
	if err != nil || len(all) != 2 {
		t.Fatalf("accept all = %#v, %v", all, err)
	}
	none, err := AcceptedFiles(offer, Decision{Accept: true, AcceptIDs: []string{}})
	if err != nil || len(none) != 0 {
		t.Fatalf("accept none = %#v, %v", none, err)
	}
	one, err := AcceptedFiles(offer, Decision{Accept: true, AcceptIDs: []string{"b"}})
	if err != nil || len(one) != 1 || one[0].ID != "b" {
		t.Fatalf("partial accept = %#v, %v", one, err)
	}
	if _, err := AcceptedFiles(offer, Decision{Accept: true, AcceptIDs: []string{"missing"}}); err == nil {
		t.Fatal("unknown accepted id was allowed")
	}
}

func TestValidateFingerprint(t *testing.T) {
	if err := ValidateFingerprint("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "abcd", "G123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"} {
		if ValidateFingerprint(value) == nil {
			t.Fatalf("invalid fingerprint %q accepted", value)
		}
	}
}
