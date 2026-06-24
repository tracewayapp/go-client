package traceway

import "testing"

func TestInitInvalidConnectionString(t *testing.T) {
	prev := collectionFrameStore
	defer func() { collectionFrameStore = prev }()

	for _, conn := range []string{"missing-at-separator", ""} {
		collectionFrameStore = nil
		if err := Init(conn); err == nil {
			t.Errorf("Init(%q) = nil error, want error for malformed connection string", conn)
		}
		if collectionFrameStore != nil {
			t.Errorf("Init(%q) initialized the store on invalid input", conn)
		}
	}
}
