package limiter

import (
	"context"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"golang.org/x/time/rate"
)

func waitForBytes(ctx context.Context, bucket *rate.Limiter, n int) error {
	burst := bucket.Burst()
	if burst <= 0 {
		return bucket.WaitN(ctx, n)
	}
	for n > 0 {
		chunk := min(n, burst)
		if err := bucket.WaitN(ctx, chunk); err != nil {
			return err
		}
		n -= chunk
	}
	return nil
}

type Reader struct {
	buf.Reader
	ctx     context.Context
	limiter *rate.Limiter
}

func (l *Limiter) RateReader(ctx context.Context, r buf.Reader, bucket *rate.Limiter) buf.Reader {
	return &Reader{Reader: r, ctx: ctx, limiter: bucket}
}

func (r *Reader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := r.Reader.ReadMultiBuffer()
	if waitErr := waitForBytes(r.ctx, r.limiter, int(mb.Len())); waitErr != nil {
		buf.ReleaseMulti(mb)
		return nil, waitErr
	}
	return mb, err
}

type Writer struct {
	writer  buf.Writer
	ctx     context.Context
	limiter *rate.Limiter
}

func (l *Limiter) RateWriter(writer buf.Writer, bucket *rate.Limiter) buf.Writer {
	return l.RateWriterContext(context.Background(), writer, bucket)
}

func (l *Limiter) RateWriterContext(ctx context.Context, writer buf.Writer, bucket *rate.Limiter) buf.Writer {
	return &Writer{writer: writer, ctx: ctx, limiter: bucket}
}

func (w *Writer) Close() error { return common.Close(w.writer) }
func (w *Writer) Interrupt()   { common.Interrupt(w.writer) }
func (w *Writer) WriteMultiBuffer(mb buf.MultiBuffer) error {
	if err := waitForBytes(w.ctx, w.limiter, int(mb.Len())); err != nil {
		buf.ReleaseMulti(mb)
		return err
	}
	return w.writer.WriteMultiBuffer(mb)
}
