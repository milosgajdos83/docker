package filewriter

import (
	"io"
	"io/ioutil"
	"os"
	"sync"
)

type FileWriter struct {
	reader   io.ReadCloser
	writer   io.WriteCloser
	f        *os.File
	lock     sync.Mutex
	filePath string
}

func open(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0600)
}

func NewFileWriter(filePath string) *FileWriter {
	r, w := io.Pipe()
	writer := &FileWriter{reader: r, writer: w, filePath: filePath}
	go writer.writeToFile()
	return writer
}

func (w *FileWriter) Write(p []byte) (int, error) {
	return w.writer.Write(p)
}

func (w *FileWriter) writeToFile() {
	var err error
	if w.f != nil {
		w.f.Close()
	}
	w.f, err = open(w.filePath)
	if err != nil {
		return
	}
	io.Copy(w.f, w.reader)
}

func (w *FileWriter) Truncate() ([]byte, error) {
	var err error
	w.lock.Lock()
	defer w.lock.Unlock()

	// Close and re-open file, makes sure nothing is being added to the file
	w.f.Close()
	w.f, err = open(w.filePath)
	if err != nil {
		return nil, err
	}

	// FIXME: Probably don't want to ReadAll here
	b, err := ioutil.ReadAll(w.f)
	if err != nil {
		return nil, err
	}

	if err := os.Truncate(w.filePath, 0); err != nil {
		return nil, err
	}
	go w.writeToFile()

	return b, nil
}

func (w *FileWriter) Close() error {
	if err := w.writer.Close(); err != nil {
		return err
	}

	if err := w.f.Close(); err != nil {
		return err
	}

	if err := w.reader.Close(); err != nil {
		return err
	}
	return nil
}
