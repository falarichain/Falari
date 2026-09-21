package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"chain/internal/client"
	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"
)

const downloadIntentID = "intent_download"

const (
	testDataShards   = 4
	testParityShards = 2
	testSegmentSize  = 40_000
)

// downloadFixture serves the chain and the storage nodes from one httptest server, so a
// download run exercises the gateway's real fetch-then-reconstruct path.
type downloadFixture struct {
	plan  wire.UploadPlan
	blobs map[string][]byte
}

func newDownloadFixture(t *testing.T, data []byte) downloadFixture {
	t.Helper()
	fixture := downloadFixture{blobs: map[string][]byte{}}
	for offset, id := int64(0), 0; offset < int64(len(data)); offset, id = offset+testSegmentSize, id+1 {
		end := offset + testSegmentSize
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		shards, err := client.EncodeShards(data[offset:end], testDataShards, testParityShards)
		if err != nil {
			t.Fatalf("encode segment %d: %v", id, err)
		}
		hashes := make([]string, len(shards))
		for i, shard := range shards {
			hashes[i] = chaincrypto.HashBytes(shard)
			if _, duplicate := fixture.blobs[hashes[i]]; duplicate {
				t.Fatalf("shard %d of segment %d repeats an earlier hash; the fixture drops shards by hash", i, id)
			}
			fixture.blobs[hashes[i]] = shard
		}
		root := chaincrypto.MerkleRoot(hashes)
		fixture.plan.Segments = append(fixture.plan.Segments, wire.SegmentPlan{
			SegmentID:   id,
			SegmentRoot: root,
			ShardHashes: hashes,
		})
		fixture.plan.SegmentRoots = append(fixture.plan.SegmentRoots, root)
	}
	fixture.plan.IntentID = downloadIntentID
	fixture.plan.FileName = "sample.bin"
	fixture.plan.FileSize = int64(len(data))
	fixture.plan.SegmentSize = testSegmentSize
	fixture.plan.Erasure = wire.ErasurePolicy{DataShards: testDataShards, ParityShards: testParityShards}
	return fixture
}

// shardPositions picks shards out of every segment by their position within the segment,
// rotated by the segment index so each segment loses a different combination.
func (f downloadFixture) shardPositions(positions ...int) map[string]bool {
	picked := map[string]bool{}
	for segmentIndex, segment := range f.plan.Segments {
		total := len(segment.ShardHashes)
		for _, position := range positions {
			picked[segment.ShardHashes[(position+segmentIndex)%total]] = true
		}
	}
	return picked
}

func (f downloadFixture) server(t *testing.T, unreachable, corrupt map[string]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := r.URL.Path; {
		case path == "/manifests/"+downloadIntentID:
			if err := json.NewEncoder(w).Encode(wire.StorageManifestResponse{
				IntentID: downloadIntentID,
				Status:   wire.StatusFinalized,
				Complete: true,
				Plan:     f.plan,
			}); err != nil {
				t.Errorf("encode manifest: %v", err)
			}
		case strings.HasPrefix(path, "/shards/"):
			hash := strings.TrimSuffix(strings.TrimPrefix(path, "/shards/"), ".bin")
			shard, ok := f.blobs[hash]
			switch {
			case !ok || unreachable[hash]:
				http.NotFound(w, r)
			case corrupt[hash]:
				w.Write(bytes.Repeat([]byte{0xee}, len(shard)))
			default:
				w.Write(shard)
			}
		default:
			http.NotFound(w, r)
		}
	}))
}

func (f downloadFixture) handler(t *testing.T, srv *httptest.Server, dataShards, parityShards int) *Handler {
	t.Helper()
	h, err := New(Config{
		ChainURL:         srv.URL,
		StorageEndpoints: []string{srv.URL},
		TmpDir:           t.TempDir(),
		DataShards:       dataShards,
		ParityShards:     parityShards,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func download(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/download/"+downloadIntentID, nil)
	req.SetPathValue("intent_id", downloadIntentID)
	rec := httptest.NewRecorder()
	h.handleDownload(rec, req)
	return rec
}

// sampleData is spread out enough that no two shards of the plan share a hash, which the
// drop-by-hash fixture relies on.
func sampleData() []byte {
	const size = 100_003
	data := make([]byte, 0, size+sha256.Size)
	var block [8]byte
	for len(data) < size {
		binary.BigEndian.PutUint64(block[:], uint64(len(data)))
		digest := sha256.Sum256(block[:])
		data = append(data, digest[:]...)
	}
	return data[:size]
}

func TestDownloadReconstructsFromSurvivingShards(t *testing.T) {
	data := sampleData()

	cases := []struct {
		name        string
		drop        []int
		bad         []int
		cfgData     int
		cfgParity   int
		clearPolicy bool
		wantStatus  int
		wantErr     string
	}{
		{
			name:       "every copy reachable",
			cfgData:    testDataShards,
			cfgParity:  testParityShards,
			wantStatus: http.StatusOK,
		},
		{
			// Two of four data shards gone is exactly the parity budget.
			name:       "lost data shards are rebuilt from parity",
			drop:       []int{1, 3},
			cfgData:    testDataShards,
			cfgParity:  testParityShards,
			wantStatus: http.StatusOK,
		},
		{
			// The gateway is configured 3+1 while the intent was uploaded 4+2.
			name:       "gateway config does not override the intent policy",
			drop:       []int{0, 2},
			cfgData:    3,
			cfgParity:  1,
			wantStatus: http.StatusOK,
		},
		{
			name:        "plans without a recorded policy fall back to the config",
			drop:        []int{1, 2},
			cfgData:     testDataShards,
			cfgParity:   testParityShards,
			clearPolicy: true,
			wantStatus:  http.StatusOK,
		},
		{
			name:       "a miner serving wrong bytes is discarded and rebuilt",
			bad:        []int{0, 3},
			cfgData:    testDataShards,
			cfgParity:  testParityShards,
			wantStatus: http.StatusOK,
		},
		{
			name:       "losing more than the parity budget still fails",
			drop:       []int{1, 2, 3},
			cfgData:    testDataShards,
			cfgParity:  testParityShards,
			wantStatus: http.StatusBadGateway,
			wantErr:    "only 3 of 6 shards reachable, 4 required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newDownloadFixture(t, data)
			if tc.clearPolicy {
				fixture.plan.Erasure = wire.ErasurePolicy{}
			}
			srv := fixture.server(t, fixture.shardPositions(tc.drop...), fixture.shardPositions(tc.bad...))
			defer srv.Close()

			rec := download(t, fixture.handler(t, srv, tc.cfgData, tc.cfgParity))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantErr != "" {
				if body := rec.Body.String(); !strings.Contains(body, tc.wantErr) {
					t.Fatalf("error body = %q, want it to contain %q", body, tc.wantErr)
				}
				return
			}
			if got := rec.Body.Bytes(); !bytes.Equal(got, data) {
				t.Fatalf("downloaded %d bytes, want the original %d (first difference at %d)",
					len(got), len(data), firstDifference(got, data))
			}
		})
	}
}

func TestDownloadRefusesWhenNoErasurePolicyIsKnown(t *testing.T) {
	fixture := newDownloadFixture(t, sampleData())
	fixture.plan.Erasure = wire.ErasurePolicy{}
	srv := fixture.server(t, nil, nil)
	defer srv.Close()

	rec := download(t, fixture.handler(t, srv, 0, 0))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	if body := rec.Body.String(); !strings.Contains(body, "no usable erasure policy") {
		t.Fatalf("error body = %q", body)
	}
}

func TestDownloadReportsShardCountAgainstPolicy(t *testing.T) {
	fixture := newDownloadFixture(t, sampleData())
	fixture.plan.Segments[0].ShardHashes = fixture.plan.Segments[0].ShardHashes[:testDataShards]
	srv := fixture.server(t, nil, nil)
	defer srv.Close()

	rec := download(t, fixture.handler(t, srv, testDataShards, testParityShards))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	if body := rec.Body.String(); !strings.Contains(body, "stored 4 shards, erasure policy 4+2 expects 6") {
		t.Fatalf("error body = %q", body)
	}
}

func firstDifference(got, want []byte) int {
	for i := 0; i < len(got) && i < len(want); i++ {
		if got[i] != want[i] {
			return i
		}
	}
	if len(got) != len(want) {
		return min(len(got), len(want))
	}
	return -1
}
