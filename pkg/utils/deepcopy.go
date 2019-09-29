package utils

import (
	"bytes"
	"encoding/gob"
)

// DeepCopy provides the method to creates a deep copy of whatever is passed to
// it and returns the copy in an interface. The returned value will need to be
// asserted to the correct type.
func DeepCopy(dst, src interface{}) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(src); err != nil {
		return err
	}
	return gob.NewDecoder(bytes.NewBuffer(buf.Bytes())).Decode(dst)
}
