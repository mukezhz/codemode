package openai

import (
	"context"
	"errors"
	"fmt"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/mukezhz/codemode/llm"
)

// DefaultEmbeddingModel is used when neither EmbedOptions.Model nor EMBEDDING_MODEL is set.
const DefaultEmbeddingModel = "text-embedding-3-small"

// EmbedOptions configures the embedder; only Model, APIKey, BaseURL, and Timeout are read.
type EmbedOptions = llm.Options

// NewEmbedder builds an OpenAI-protocol embedder; it returns llm.ErrNoProvider when neither a key nor a base URL is set.
func NewEmbedder(options EmbedOptions) (llm.Embedder, error) {
	resolved, err := options.WithDefaults(llm.Defaults{Model: DefaultEmbeddingModel})
	if err != nil {
		return llm.Embedder{}, err
	}
	key, endpoint := resolved.APIKey, resolved.BaseURL
	if key == "" && endpoint == "" {
		return llm.Embedder{}, fmt.Errorf("%w: set Options.APIKey or Options.BaseURL", llm.ErrNoProvider)
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
