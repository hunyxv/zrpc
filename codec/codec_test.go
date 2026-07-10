package codec

import "testing"

type samplePayload struct {
	ID   int    `json:"id" msgpack:"id"`
	Name string `json:"name" msgpack:"name"`
}

func TestMsgpackRoundTrip(t *testing.T) {
	c := Msgpack()
	if c.Name() != "msgpack" {
		t.Fatalf("Name() = %q, want %q", c.Name(), "msgpack")
	}

	in := samplePayload{ID: 42, Name: "alice"}
	data, err := c.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var out samplePayload
	if err := c.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if out != in {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	c := JSON()
	if c.Name() != "json" {
		t.Fatalf("Name() = %q, want %q", c.Name(), "json")
	}

	in := samplePayload{ID: 7, Name: "bob"}
	data, err := c.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var out samplePayload
	if err := c.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if out != in {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
}

func TestJSONUnmarshalError(t *testing.T) {
	var out samplePayload
	if err := JSON().Unmarshal([]byte("{"), &out); err == nil {
		t.Fatal("JSON().Unmarshal malformed payload returned nil error")
	}
}

func TestMsgpackUnmarshalError(t *testing.T) {
	var out samplePayload
	if err := Msgpack().Unmarshal([]byte{0xc1}, &out); err == nil {
		t.Fatal("Msgpack().Unmarshal malformed payload returned nil error")
	}
}
