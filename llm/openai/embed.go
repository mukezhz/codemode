package openai

import (
	"context"
	"errors"
	"fmt"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/mukezhz/code-mode/llm"
)

// DefaultEmbeddingModel is used when neither EmbedOptions.Model nor
// EMBEDDING_MODEL is set.
const DefaultEmbeddingModel = "text-embedding-3-small"

// EmbedOptions configures the embedder. Every field is optional and falls back
// to the environment, then to the embedding-specific defaults.
//
// Only Model, APIKey, BaseURL, and Timeout are read; sampling parameters have
// no meaning for an embeddings request.
type EmbedOptions = llm.Options

// NewEmbedder builds an embedder over the OpenAI embeddings protocol. Anthropic
// publishes no embeddings API, so this path is used regardless of LLM_PROVIDER;
// point BaseURL or EMBEDDING_BASE_URL at any compatible endpoint to embed
// locally.
//
// Options left zero fall back to the environment:
//
//	EMBEDDING_MODEL     (default text-embedding-3-small)
//	EMBEDDING_API_KEY   (falls back to OPENAI_API_KEY)
//	EMBEDDING_BASE_URL  (falls back to OPENAI_BASE_URL, then LLM_BASE_URL)
//
// It returns llm.ErrNoProvider when neither a key nor a base URL is configured.
func NewEmbedder(options EmbedOptions) (llm.Embedder, error) {
	resolved, err := options.Resolve(llm.Defaults{
		Model:      DefaultEmbeddingModel,
		ModelEnv:   "EMBEDDING_MODEL",
		APIKeyEnv:  "EMBEDDING_API_KEY",
		BaseURLEnv: "EMBEDDING_BASE_URL",
	})
	if err != nil {
		return llm.Embedder{}, err
	}
	// The embedding variables win, but an application that configured only the
	// OpenAI ones should not have to repeat them.
	key := resolved.APIKey
	if key == "" {
		key = llm.Env("OPENAI_API_KEY", "")
	}
	endpoint := resolved.BaseURL
	if endpoint == "" {
		endpoint = llm.BaseURL("OPENAI_BASE_URL")
	}
	if key == "" && endpoint == "" {
		return llm.Embedder{}, fmt.Errorf("%w: set EMBEDDING_API_KEY or EMBEDDING_BASE_URL", llm.ErrNoProvider)
	}

	requestOptions := []option.RequestOption{option.WithAPIKey(key)}
	if endpoint != "" {
		requestOptions = append(requestOptions, option.WithBaseURL(endpoint))
	}
	if resolved.Timeout > 0 {
		requestOptions = append(requestOptions, option.WithRequestTimeout(resolved.Timeout))
	}
	client := openaisdk.NewClient(requestOptions...)
	model := resolved.Model

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
