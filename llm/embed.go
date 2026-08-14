package llm

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// defaultEmbeddingModel is used when EMBEDDING_MODEL is unset.
const defaultEmbeddingModel = "text-embedding-3-small"

// Embedder turns text into a vector for capability search.
type Embedder struct {
	// Model is recorded in the capability index, so a later search with a
	// different model is rejected rather than silently mismatched.
	Model string
	// Embed produces the vector. Ctx carries cancellation.
	Embed func(ctx context.Context, text string) ([]float32, error)
}

// NewEmbedder builds an embedder over the OpenAI embeddings protocol. Anthropic
// publishes no embeddings API, so this path is used regardless of LLM_PROVIDER;
// point EMBEDDING_BASE_URL at any compatible endpoint to embed locally.
//
// Environment:
//
//	EMBEDDING_MODEL     (default text-embedding-3-small)
//	EMBEDDING_API_KEY   (falls back to OPENAI_API_KEY)
//	EMBEDDING_BASE_URL  (falls back to OPENAI_BASE_URL, then LLM_BASE_URL)
//
// It returns ErrNoProvider when neither a key nor a base URL is configured.
func NewEmbedder() (Embedder, error) {
	key := env("EMBEDDING_API_KEY", os.Getenv("OPENAI_API_KEY"))
	endpoint := env("EMBEDDING_BASE_URL", baseURL("OPENAI_BASE_URL"))
	if key == "" && endpoint == "" {
		return Embedder{}, fmt.Errorf("%w: set EMBEDDING_API_KEY or EMBEDDING_BASE_URL", ErrNoProvider)
	}

	options := []option.RequestOption{option.WithAPIKey(key)}
	if endpoint != "" {
		options = append(options, option.WithBaseURL(endpoint))
	}
	client := openai.NewClient(options...)
	model := env("EMBEDDING_MODEL", defaultEmbeddingModel)

	return Embedder{
		Model: model,
		Embed: func(ctx context.Context, text string) ([]float32, error) {
			response, err := client.Embeddings.New(ctx, openai.EmbeddingNewParams{
				Model: openai.EmbeddingModel(model),
				Input: openai.EmbeddingNewParamsInputUnion{OfString: openai.String(text)},
			})
			if err != nil {
				return nil, fmt.Errorf("embed: %w", err)
			}
			if len(response.Data) == 0 {
				return nil, errors.New("embed: no embedding returned")
			}
			// The API returns float64; the index stores float32.
			embedding := response.Data[0].Embedding
			vector := make([]float32, len(embedding))
			for i, value := range embedding {
				vector[i] = float32(value)
			}
			return vector, nil
		},
	}, nil
}
