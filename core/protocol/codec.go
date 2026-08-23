package protocol

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// 线路帧格式（frame）：
//
//	┌────────┬───────────────────────┐
//	│ 1 byte │  frameKind             │
//	├────────┼───────────────────────┤
//	│ 8 byte │  length (big-endian)   │
//	├────────┼───────────────────────┤
//	│ length │  body                  │
//	└────────┴───────────────────────┘
//
// frameKind 为 frameControl 时 body 是一个 JSON 编码的 Envelope；
// 为 frameBinary 时 body 是文件的原始字节（跟在一条 file_start 控制帧之后）。
// 长度前缀让接收方无需依赖分隔符即可精确读取每一帧，天然支持大文件流式传输。

type frameKind byte

const (
	frameControl frameKind = 1
	frameBinary  frameKind = 2
)

// maxControlFrame 限制单条控制帧大小，避免恶意/异常输入导致内存爆掉。
// 清单可能较大（一次发送数万个文件），这里给到 64MiB。二进制帧不受此限制，走流式读取。
const maxControlFrame = 64 << 20

// maxBinaryFrame 限制单个文件大小（16GiB），足够覆盖大视频/磁盘镜像，同时防止溢出。
const maxBinaryFrame = 16 << 30

var errFrameTooLarge = errors.New("protocol: frame exceeds maximum allowed size")

// Writer 向底层连接写入协议帧，内部带缓冲。并发调用不安全，需由上层串行化。
type Writer struct {
	w   *bufio.Writer
	hdr [9]byte
}

// NewWriter 基于任意 io.Writer 构造 Writer。
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: bufio.NewWriter(w)}
}

// WriteControl 编码并写出一条控制消息。
func (fw *Writer) WriteControl(t MsgType, payload any) error {
	var raw []byte
	if payload != nil {
		var err error
		raw, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("protocol: marshal payload: %w", err)
		}
	}
	env := Envelope{Type: t, Payload: raw}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("protocol: marshal envelope: %w", err)
	}
	if len(body) > maxControlFrame {
		return errFrameTooLarge
	}
	if err := fw.writeHeader(frameControl, uint64(len(body))); err != nil {
		return err
	}
	if _, err := fw.w.Write(body); err != nil {
		return err
	}
	return fw.w.Flush()
}

// WriteBinary 以流式方式写出一个二进制帧，从 src 拷贝恰好 size 字节。
// 用于推送文件原始字节，避免把整份文件读进内存。
func (fw *Writer) WriteBinary(src io.Reader, size int64) error {
	if size < 0 || size > maxBinaryFrame {
		return errFrameTooLarge
	}
	if err := fw.writeHeader(frameBinary, uint64(size)); err != nil {
		return err
	}
	n, err := io.CopyN(fw.w, src, size)
	if err != nil {
		return fmt.Errorf("protocol: write binary body: %w", err)
	}
	if n != size {
		return fmt.Errorf("protocol: short binary write: wrote %d of %d", n, size)
	}
	return fw.w.Flush()
}

func (fw *Writer) writeHeader(kind frameKind, length uint64) error {
	fw.hdr[0] = byte(kind)
	binary.BigEndian.PutUint64(fw.hdr[1:], length)
	_, err := fw.w.Write(fw.hdr[:])
	return err
}

// Reader 从底层连接读取协议帧，内部带缓冲。并发调用不安全，需由上层串行化。
type Reader struct {
	r   *bufio.Reader
	hdr [9]byte
}

// NewReader 基于任意 io.Reader 构造 Reader。
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 64<<10)}
}

// Frame 是从连接上读到的一帧。IsBinary 为 true 时表示这是文件原始字节帧，
// 应调用 Reader.ReadBinaryBody 消费其内容；否则表示这是一条控制消息，已解码到 Control。
type Frame struct {
	IsBinary bool
	// Control 在 IsBinary 为 false 时有效。
	Control Envelope
	// binaryLen 在 IsBinary 为 true 时表示待读取的字节数。
	binaryLen int64
}

// ReadFrame 读取下一帧的帧头。
//   - 若是控制帧，会连同 body 一并读出并解码进 Frame.Control。
//   - 若是二进制帧，仅返回帧头信息（含长度），随后必须调用 ReadBinaryBody
//     把 body 流式写入目标，然后才能继续读下一帧。
func (fr *Reader) ReadFrame() (*Frame, error) {
	if _, err := io.ReadFull(fr.r, fr.hdr[:]); err != nil {
		return nil, err
	}
	kind := frameKind(fr.hdr[0])
	length := binary.BigEndian.Uint64(fr.hdr[1:])

	switch kind {
	case frameControl:
		if length > maxControlFrame {
			return nil, errFrameTooLarge
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(fr.r, body); err != nil {
			return nil, err
		}
		var env Envelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("protocol: unmarshal envelope: %w", err)
		}
		return &Frame{IsBinary: false, Control: env}, nil
	case frameBinary:
		if length > maxBinaryFrame {
			return nil, errFrameTooLarge
		}
		return &Frame{IsBinary: true, binaryLen: int64(length)}, nil
	default:
		return nil, fmt.Errorf("protocol: unknown frame kind %d", kind)
	}
}

// ReadBinaryBody 把二进制帧的 body 恰好拷贝 size 字节到 dst。
// 只能在 ReadFrame 返回了 IsBinary 帧后调用，且必须完整消费，否则后续帧会错位。
func (fr *Reader) ReadBinaryBody(f *Frame, dst io.Writer) error {
	if !f.IsBinary {
		return errors.New("protocol: ReadBinaryBody called on control frame")
	}
	n, err := io.CopyN(dst, fr.r, f.binaryLen)
	if err != nil {
		return fmt.Errorf("protocol: read binary body: %w", err)
	}
	if n != f.binaryLen {
		return fmt.Errorf("protocol: short binary read: got %d of %d", n, f.binaryLen)
	}
	return nil
}

// BinaryLen 返回二进制帧待读取的字节数。
func (f *Frame) BinaryLen() int64 { return f.binaryLen }

// DecodePayload 把控制帧信封里的 Payload 反序列化到 v。
func DecodePayload(env Envelope, v any) error {
	if len(env.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(env.Payload, v)
}
