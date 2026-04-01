package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/vesperarch/gopherdoc/pkg/document"
	"github.com/vesperarch/gopherdoc/pkg/engine"
)

type fakeParser struct{}

func (fakeParser) Parse(_ context.Context, r interface{ Read([]byte) (int, error) }) (*document.Document, error) {
	buf := make([]byte, 1024)
	n, err := r.Read(buf)
	if err != nil && err.Error() != "EOF" {
		return nil, fmt.Errorf("fakeparser: read: %w", err)
	}
	return &document.Document{
		ID:      fmt.Sprintf("doc-%d", time.Now().UnixNano()),
		Content: buf[:n],
		Metadata: map[string]any{
			"parser": "fake",
		},
	}, nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p := fakeParser{}
	d := engine.NewDispatcher(10)
	d.Start(ctx, 4)

	for i := range 10 {
		i := i
		if err := d.Submit(ctx, engine.Job{
			ID: fmt.Sprintf("job-%d", i),
			Execute: func(ctx context.Context) error {
				r := strings.NewReader(fmt.Sprintf("conteúdo do documento %d", i))
				doc, err := p.Parse(ctx, r)
				if err != nil {
					return fmt.Errorf("ingestion: doc %d: %w", i, err)
				}
				fmt.Printf("[job-%d] id=%s content=%q\n", i, doc.ID, doc.Content)
				return nil
			},
		}); err != nil {
			log.Fatalf("submit job-%d: %v", i, err)
		}
	}

	d.Close()
	d.Wait()

	if errs := d.Errs(); len(errs) > 0 {
		for _, e := range errs {
			log.Printf("ERROR: %v", e)
		}
	} else {
		fmt.Println("\nall jobs completed successfully")
	}
}
