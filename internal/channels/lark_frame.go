package channels

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
	"strconv"
	"time"
)

// Only the published websocket frame fields are needed. This bounded wire
// codec avoids importing the generated SDK's entire API tree.
type LarkFrame struct {
	Seq, Log, Service, Method uint64
	Headers                   map[string]string
	Encoding, Type            string
	Payload, LogNew           []byte
}

func wireVar(out []byte, field, value uint64) []byte {
	out = binary.AppendUvarint(out, field<<3)
	return binary.AppendUvarint(out, value)
}
func wireBytes(out []byte, field uint64, value []byte) []byte {
	out = binary.AppendUvarint(out, field<<3|2)
	out = binary.AppendUvarint(out, uint64(len(value)))
	return append(out, value...)
}
func (f LarkFrame) Marshal() []byte {
	var out []byte
	out = wireVar(out, 1, f.Seq)
	out = wireVar(out, 2, f.Log)
	out = wireVar(out, 3, f.Service)
	out = wireVar(out, 4, f.Method)
	keys := make([]string, 0, len(f.Headers))
	for k := range f.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		h := wireBytes(nil, 1, []byte(key))
		h = wireBytes(h, 2, []byte(f.Headers[key]))
		out = wireBytes(out, 5, h)
	}
	out = wireBytes(out, 6, []byte(f.Encoding))
	out = wireBytes(out, 7, []byte(f.Type))
	out = wireBytes(out, 8, f.Payload)
	return wireBytes(out, 9, f.LogNew)
}
func wireRead(raw []byte, call func(uint64, uint64, []byte) error) error {
	for len(raw) > 0 {
		tag, n := binary.Uvarint(raw)
		if n <= 0 || tag>>3 == 0 {
			return errors.New("lark: invalid protobuf tag")
		}
		raw = raw[n:]
		var v uint64
		var b []byte
		switch tag & 7 {
		case 0:
			v, n = binary.Uvarint(raw)
			if n <= 0 {
				return errors.New("lark: invalid varint")
			}
			raw = raw[n:]
		case 2:
			size, n := binary.Uvarint(raw)
			if n <= 0 || size > uint64(len(raw)-n) {
				return errors.New("lark: invalid field length")
			}
			raw = raw[n:]
			b = raw[:int(size)]
			raw = raw[int(size):]
		case 1:
			if len(raw) < 8 {
				return errors.New("lark: truncated field")
			}
			raw = raw[8:]
		case 5:
			if len(raw) < 4 {
				return errors.New("lark: truncated field")
			}
			raw = raw[4:]
		default:
			return errors.New("lark: unsupported protobuf wire type")
		}
		if err := call(tag>>3, v, b); err != nil {
			return err
		}
	}
	return nil
}
func DecodeLarkFrame(raw []byte) (f LarkFrame, err error) {
	f.Headers = map[string]string{}
	if len(raw) > 1<<20 {
		return f, errors.New("lark: frame exceeds limit")
	}
	err = wireRead(raw, func(field, v uint64, b []byte) error {
		switch field {
		case 1:
			f.Seq = v
		case 2:
			f.Log = v
		case 3:
			f.Service = v
		case 4:
			f.Method = v
		case 5:
			if len(f.Headers) >= 64 || len(b) > 4096 {
				return errors.New("lark: headers exceed limit")
			}
			var k, value string
			err := wireRead(b, func(field, v uint64, b []byte) error {
				if field == 1 {
					k = string(b)
				}
				if field == 2 {
					value = string(b)
				}
				return nil
			})
			if err != nil {
				return err
			}
			if _, exists := f.Headers[k]; exists {
				return errors.New("lark: repeated header")
			}
			f.Headers[k] = value
		case 6:
			f.Encoding = string(b)
		case 7:
			f.Type = string(b)
		case 8:
			f.Payload = append([]byte(nil), b...)
		case 9:
			f.LogNew = append([]byte(nil), b...)
		}
		return nil
	})
	if f.Encoding != "" && f.Encoding != "json" {
		return f, errors.New("lark: unsupported payload encoding")
	}
	return f, err
}

type larkParts struct {
	at      time.Time
	parts   [][]byte
	size    int
	service uint64
}
type LarkAssembler struct {
	entries map[string]*larkParts
	now     func() time.Time
}

func NewLarkAssembler() *LarkAssembler {
	return &LarkAssembler{entries: map[string]*larkParts{}, now: time.Now}
}
func (a *LarkAssembler) Add(f LarkFrame) ([]byte, bool, error) {
	sum, err := strconv.Atoi(f.Headers["sum"])
	if f.Headers["sum"] == "" {
		sum = 1
		err = nil
	}
	if err != nil || sum < 1 || sum > 64 {
		return nil, false, errors.New("lark: invalid fragment count")
	}
	if sum == 1 {
		return f.Payload, true, nil
	}
	seq, err := strconv.Atoi(f.Headers["seq"])
	id := f.Headers["message_id"]
	if err != nil || seq < 0 || seq >= sum || !validID(id) {
		return nil, false, errors.New("lark: invalid fragment")
	}
	now := a.now()
	for k, v := range a.entries {
		if now.Sub(v.at) > 5*time.Second {
			delete(a.entries, k)
		}
	}
	entry := a.entries[id]
	if entry == nil {
		if len(a.entries) >= 16 {
			return nil, false, errors.New("lark: fragment capacity reached")
		}
		entry = &larkParts{at: now, parts: make([][]byte, sum), service: f.Service}
		a.entries[id] = entry
	}
	if len(entry.parts) != sum || entry.service != f.Service {
		return nil, false, errors.New("lark: inconsistent fragments")
	}
	if entry.parts[seq] != nil {
		if !bytes.Equal(entry.parts[seq], f.Payload) {
			return nil, false, errors.New("lark: conflicting fragment")
		}
	} else {
		entry.size += len(f.Payload)
		if entry.size > 1<<20 {
			delete(a.entries, id)
			return nil, false, errors.New("lark: combined payload exceeds limit")
		}
		entry.parts[seq] = append([]byte{}, f.Payload...)
	}
	for _, part := range entry.parts {
		if part == nil {
			return nil, false, nil
		}
	}
	out := make([]byte, 0, entry.size)
	for _, part := range entry.parts {
		out = append(out, part...)
	}
	delete(a.entries, id)
	return out, true, nil
}
