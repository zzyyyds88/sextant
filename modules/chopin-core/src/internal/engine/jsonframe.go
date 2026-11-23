package engine

import "encoding/json"

// jsonFrame renders any value as one SSE data frame.
func jsonFrame(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(data)+8)
	out = append(out, "data: "...)
	out = append(out, data...)
	out = append(out, '\n', '\n')
	return out, nil
}
