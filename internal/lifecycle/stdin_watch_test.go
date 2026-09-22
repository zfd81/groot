package lifecycle

import (
	"errors"
	"os"
	"testing"
	"testing/iotest"
	"time"
)

func TestWatchStdin_TriggersOnEOF(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fired := make(chan struct{}, 1)
	WatchStdin(r, func() { fired <- struct{}{} })

	select {
	case <-fired:
		t.Fatal("写端未关闭时不应触发")
	case <-time.After(50 * time.Millisecond):
	}

	// 写入数据不应触发：监听只关心 EOF
	if _, err := w.Write([]byte("noise\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
		t.Fatal("写入数据不应触发")
	case <-time.After(50 * time.Millisecond):
	}

	w.Close()
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("关闭写端后应触发回调")
	}
}

func TestWatchStdin_TriggersOnReadError(t *testing.T) {
	fired := make(chan struct{}, 1)
	WatchStdin(iotest.ErrReader(errors.New("boom")), func() { fired <- struct{}{} })
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("读错误应触发回调")
	}
}
