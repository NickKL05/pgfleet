// Package fingerprint hashes a flattened schema model into per-object hashes
// and a single schema fingerprint. The fingerprint is independent of schema
// name, ordering of maps, and any non-structural noise, so two structurally
// identical tenants produce the same fingerprint (spec 5.2).
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/NickKL05/pgfleet/internal/drift/model"
)

// ObjectHash is the stable identity of one object plus the hash of its body.
type ObjectHash struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Hash string `json:"hash"`
}

// Fingerprint is the full structural fingerprint of a schema: the per-object
// hashes (retained for diffing) and the combined schema hash.
type Fingerprint struct {
	Objects []ObjectHash `json:"objects"`
	Hash    string       `json:"hash"`
}

// Compute hashes each flattened object, then hashes the sorted per-object
// hashes into one schema fingerprint. The input is expected to already be
// sorted by (type, name); model.Schema.Flatten guarantees that.
func Compute(objects []model.FlatObject) Fingerprint {
	fp := Fingerprint{Objects: make([]ObjectHash, 0, len(objects))}

	roll := sha256.New()
	for _, o := range objects {
		h := hashObject(o)
		fp.Objects = append(fp.Objects, ObjectHash{Type: o.Type, Name: o.Name, Hash: h})

		// Fold identity and object hash into the schema roll-up. The separators
		// keep the stream unambiguous.
		roll.Write([]byte(o.Type))
		roll.Write([]byte{0})
		roll.Write([]byte(o.Name))
		roll.Write([]byte{0})
		roll.Write([]byte(h))
		roll.Write([]byte{'\n'})
	}
	fp.Hash = hex.EncodeToString(roll.Sum(nil))
	return fp
}

func hashObject(o model.FlatObject) string {
	h := sha256.New()
	h.Write([]byte(o.Type))
	h.Write([]byte{0})
	h.Write([]byte(o.Name))
	h.Write([]byte{0})
	h.Write([]byte(o.Body))
	return hex.EncodeToString(h.Sum(nil))
}

// index builds a name-keyed lookup of a fingerprint's objects, keyed by the
// "type\x00name" identity.
func index(fp Fingerprint) map[string]ObjectHash {
	m := make(map[string]ObjectHash, len(fp.Objects))
	for _, o := range fp.Objects {
		m[o.Type+"\x00"+o.Name] = o
	}
	return m
}
