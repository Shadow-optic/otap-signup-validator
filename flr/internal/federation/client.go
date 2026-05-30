// Package federation provides cross-operator federation capabilities for the FLR.
package federation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/otap/flr/internal/models"
	"github.com/otap/flr/internal/registry"
)

// Client makes outbound HTTP calls to peer operators.
type Client struct {
	httpClient *http.Client
	timeout    time.Duration
}

// NewClient creates a federation client with the given request timeout.
func NewClient(timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: timeout,
		},
		timeout: timeout,
	}
}

// GetCommitment fetches a Merkle commitment from a peer operator.
func (c *Client) GetCommitment(peerEndpoint, operatorID string, blockHeight int64) (*models.MerkleCommitment, error) {
	url := fmt.Sprintf("%s/v1/commitments/%s/%d", peerEndpoint, operatorID, blockHeight)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create commitment request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch commitment from %s: %w", peerEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("commitment request failed: %s (status %d)", string(body), resp.StatusCode)
	}

	var commitment models.MerkleCommitment
	if err := json.NewDecoder(resp.Body).Decode(&commitment); err != nil {
		return nil, fmt.Errorf("decode commitment: %w", err)
	}
	return &commitment, nil
}

// GetLeases fetches leases from a peer operator matching the provided filter.
func (c *Client) GetLeases(peerEndpoint string, filter registry.LeaseFilter) ([]*models.Lease, error) {
	url := fmt.Sprintf("%s/v1/leases", peerEndpoint)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create leases request: %w", err)
	}

	q := req.URL.Query()
	if filter.OperatorID != "" {
		q.Set("operator_id", filter.OperatorID)
	}
	if filter.EndpointID != "" {
		q.Set("endpoint_id", filter.EndpointID)
	}
	if filter.Status != 0 {
		q.Set("status", fmt.Sprintf("%d", filter.Status))
	}
	req.URL.RawQuery = q.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch leases from %s: %w", peerEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("leases request failed: %s (status %d)", string(body), resp.StatusCode)
	}

	var leases []*models.Lease
	if err := json.NewDecoder(resp.Body).Decode(&leases); err != nil {
		return nil, fmt.Errorf("decode leases: %w", err)
	}
	return leases, nil
}

// PushCommitment sends a Merkle commitment to a peer operator.
func (c *Client) PushCommitment(peerEndpoint string, commitment *models.MerkleCommitment) error {
	url := fmt.Sprintf("%s/v1/commitments", peerEndpoint)
	body, err := json.Marshal(commitment)
	if err != nil {
		return fmt.Errorf("marshal commitment: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create push commitment request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("push commitment to %s: %w", peerEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("push commitment failed: %s (status %d)", string(respBody), resp.StatusCode)
	}
	return nil
}

// SubmitProofOfInvalidity sends a ProofOfInvalidity to a peer operator.
func (c *Client) SubmitProofOfInvalidity(peerEndpoint string, poi *models.ProofOfInvalidity) error {
	url := fmt.Sprintf("%s/v1/invalidity", peerEndpoint)
	body, err := json.Marshal(poi)
	if err != nil {
		return fmt.Errorf("marshal proof of invalidity: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create PoI request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("submit PoI to %s: %w", peerEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("submit PoI failed: %s (status %d)", string(respBody), resp.StatusCode)
	}
	return nil
}

// StreamUpdates opens a Server-Sent Events connection for real-time registry updates.
// Returns two channels: one for updates and one for errors.
func (c *Client) StreamUpdates(peerEndpoint string, fromHeight int64) (<-chan *models.RegistryUpdate, <-chan error) {
	updates := make(chan *models.RegistryUpdate, 10)
	errors := make(chan error, 1)

	go c.streamUpdates(peerEndpoint, fromHeight, updates, errors)

	return updates, errors
}

func (c *Client) streamUpdates(peerEndpoint string, fromHeight int64, updates chan *models.RegistryUpdate, errCh chan<- error) {
	defer close(errCh)
	defer close(updates)

	url := fmt.Sprintf("%s/v1/stream?from_block_height=%d", peerEndpoint, fromHeight)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		errCh <- fmt.Errorf("create stream request: %w", err)
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		errCh <- fmt.Errorf("open stream to %s: %w", peerEndpoint, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errCh <- fmt.Errorf("stream request failed with status %d", resp.StatusCode)
		return
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				errCh <- fmt.Errorf("read stream: %w", err)
			}
			return
		}

		// SSE data lines start with "data: "
		const prefix = "data: "
		if len(line) > len(prefix) && line[:len(prefix)] == prefix {
			data := strings.TrimRight(line[len(prefix):], "\r\n")
			var update models.RegistryUpdate
			if err := json.Unmarshal([]byte(data), &update); err != nil {
				errCh <- fmt.Errorf("decode stream update: %w", err)
				continue
			}
			select {
			case updates <- &update:
			default:
				// Channel full, drop oldest
				select {
				case <-updates:
				default:
				}
				updates <- &update
			}
		}
	}
}

// RegisterOperator registers our operator with a peer.
func (c *Client) RegisterOperator(peerEndpoint string, op *models.Operator) error {
	url := fmt.Sprintf("%s/v1/operators", peerEndpoint)
	body, err := json.Marshal(op)
	if err != nil {
		return fmt.Errorf("marshal operator: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create register request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("register operator with %s: %w", peerEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register operator failed: %s (status %d)", string(respBody), resp.StatusCode)
	}
	return nil
}

// GetOperator fetches operator information from a peer.
func (c *Client) GetOperator(peerEndpoint, operatorID string) (*models.Operator, error) {
	url := fmt.Sprintf("%s/v1/operators/%s", peerEndpoint, operatorID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create get operator request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get operator from %s: %w", peerEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("operator %s not found at %s", operatorID, peerEndpoint)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get operator failed: %s (status %d)", string(body), resp.StatusCode)
	}

	var op models.Operator
	if err := json.NewDecoder(resp.Body).Decode(&op); err != nil {
		return nil, fmt.Errorf("decode operator: %w", err)
	}
	return &op, nil
}

// GetTranslationTable fetches the translation table from a peer operator.
func (c *Client) GetTranslationTable(peerEndpoint, operatorID string) ([]*models.TranslationEntry, error) {
	url := fmt.Sprintf("%s/v1/translations?operator_id=%s", peerEndpoint, operatorID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create translation request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch translations from %s: %w", peerEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("translation request failed: %s (status %d)", string(body), resp.StatusCode)
	}

	var entries []*models.TranslationEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode translations: %w", err)
	}
	return entries, nil
}

// HealthCheck checks if a peer is reachable via its health endpoint.
func (c *Client) HealthCheck(peerEndpoint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/health", peerEndpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create health request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check %s: %w", peerEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer %s unhealthy (status %d)", peerEndpoint, resp.StatusCode)
	}
	return nil
}
