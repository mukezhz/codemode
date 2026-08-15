package openai

import (
	"context"
	"errors"
	"fmt"
	"os"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/mukezhz/code-mode/llm"
)

// defaultEmbeddingModel is used when EMBEDDING_MODEL is unset.
const defaultEmbeddingModel = "text-embedding-3-small"

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
// It returns llm.ErrNoProvider when neither a key nor a base URL is configured.
func NewEmbedder() (llm.Embedder, error) {
	key := llm.Env("EMBEDDING_API_KEY", os.Getenv("OPENAI_API_KEY"))
	endpoint := llm.Env("EMBEDDING_BASE_URL", llm.BaseURL("OPENAI_BASE_URL"))
	if key == "" && endpoint == "" {
		return llm.Embedder{}, fmt.Errorf("%w: set EMBEDDING_API_KEY or EMBEDDING_BASE_URL", llm.ErrNoProvider)
	}

	options := []option.RequestOption{option.WithAPIKey(key)}
	if endpoint != "" {
		options = append(options, option.WithBaseURL(endpoint))
	}
	client := openaisdk.NewClient(options...)
	model := llm.Env("EMBEDDING_MODEL", defaultEmbeddingModel)

	return llm.Embedder{
		Model: model,
		Embed: func(ctx context.Context, text string) ([]float32, error) {
			response, err := client.Embeddings.New(ctx, openaisdk.EmbeddingNewParams{
				Model: openaisdk.EmbeddingModel(model),
				Input: openaisdk.EmbeddingNewParamsInputUnion{OfString: openaisdk.String(text)},
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
