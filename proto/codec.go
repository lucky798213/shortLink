package proto

import (
	"encoding/json"

	"google.golang.org/grpc/encoding"
)

const shortURLCodecName = "json"

type shortURLJSONCodec struct{}

func (shortURLJSONCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (shortURLJSONCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (shortURLJSONCodec) Name() string {
	return shortURLCodecName
}

func init() {
	encoding.RegisterCodec(shortURLJSONCodec{})
}
