package wire

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// TestGovernanceProposalSignatureCoversEveryField walks
// CreateGovernanceProposalRequest with reflection, flips one field at a time and
// requires the signature to stop verifying. A target that is absent from the
// canonical signing payload can be changed by anyone relaying the request without
// invalidating the proposer's signature, which is what happened to the
// registration-bonus and activation-window targets.
func TestGovernanceProposalSignatureCoversEveryField(t *testing.T) {
	priv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := AccountAddress(&priv.PublicKey)

	req := populatedGovernanceProposalRequest(t, addr)
	if err := SignGovernanceProposal(&req, priv); err != nil {
		t.Fatalf("sign populated proposal: %v", err)
	}
	if err := VerifyGovernanceProposal(req, addr); err != nil {
		t.Fatalf("populated proposal does not verify: %v", err)
	}

	payloadType := reflect.TypeOf(governanceProposalSigningPayload{})
	payloadFields := signingPayloadJSONTags(payloadType)
	payloadKeyCount, err := governanceProposalPayloadKeyCount(req)
	if err != nil {
		t.Fatal(err)
	}
	// encoding/json drops both fields when two carry the same key, which would
	// silently un-sign one of them.
	if payloadKeyCount != payloadType.NumField() {
		t.Errorf("payload encodes %d keys, signing struct has %d fields", payloadKeyCount, payloadType.NumField())
	}
	requestType := reflect.TypeOf(req)

	for i := 0; i < requestType.NumField(); i++ {
		field := requestType.Field(i)
		if field.Name == "Signature" {
			continue // the signature is what is being verified
		}
		tag := jsonFieldTag(field)
		if !payloadFields[tag] {
			t.Errorf("%s (%s) is missing from the proposal signing payload", field.Name, tag)
			continue
		}

		tampered := req
		tamperValue(reflect.ValueOf(&tampered).Elem().Field(i))
		if reflect.DeepEqual(tampered, req) {
			t.Fatalf("tamperValue did not change %s", field.Name)
		}
		if err := VerifyGovernanceProposal(tampered, addr); err == nil {
			t.Errorf("changing %s (%s) left the proposal signature valid", field.Name, tag)
		}
	}
}

// TestGovernanceProposalSigningPayloadGoldenVector pins the exact bytes hashed
// for a proposal. The governance web client rebuilds this payload in TypeScript,
// so any change to the field set or order must be mirrored there; when this test
// fails deliberately, update governance/src/lib/signing.ts in the same change.
func TestGovernanceProposalSigningPayloadGoldenVector(t *testing.T) {
	req := CreateGovernanceProposalRequest{
		Proposer:                         "0x1111111111111111111111111111111111111111",
		ChainID:                          "falari-devnet",
		IntentID:                         "intent_01",
		Action:                           "update_mining_params",
		ReasonHash:                       "abcd",
		TargetPermanentFundInjectionBPS:  2800,
		TargetRegistrationBonusAmount:    5000000000,
		TargetActivationWindowSeconds:    259200,
		TargetFeeMarketBaseFee:           1000,
		TargetFeeMultiplierBridgeOut:     15000,
		TargetDataModerationThresholdNum: 1,
		TargetDataModerationThresholdDen: 3,
		TargetPermissions:                []string{"admin", "upgrade"},
		Nonce:                            7,
		CreatedAtUnix:                    1770000000,
	}
	want := `{"proposer":"0x1111111111111111111111111111111111111111",` +
		`"chain_id":"falari-devnet","intent_id":"intent_01","action":"update_mining_params",` +
		`"reason_hash":"abcd","target_permissions":["admin","upgrade"],` +
		`"target_data_moderation_threshold_num":1,"target_data_moderation_threshold_den":3,` +
		`"target_permanent_fund_injection_bps":2800,` +
		`"target_registration_bonus_amount":5000000000,"target_activation_window_seconds":259200,` +
		`"target_fee_market_base_fee":1000,"target_fee_multiplier_bridge_out":15000,` +
		`"nonce":7,"created_at_unix":1770000000}`

	data, err := GovernanceProposalPayload(req)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("proposal signing payload bytes changed:\n got %s\nwant %s", data, want)
	}

	vote := `{"proposal_id":"gov_01","voter":"0x1111111111111111111111111111111111111111",` +
		`"approve":false,"chain_id":"falari-devnet","nonce":0,"created_at_unix":1770000000}`
	voteData, err := GovernanceVotePayload(CastGovernanceVoteRequest{
		ProposalID:    "gov_01",
		Voter:         "0x1111111111111111111111111111111111111111",
		Approve:       false,
		ChainID:       "falari-devnet",
		CreatedAtUnix: 1770000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(voteData) != vote {
		t.Fatalf("vote signing payload bytes changed:\n got %s\nwant %s", voteData, vote)
	}

	// Only the digest is exported for execute requests, so pin that one.
	executeReq := ExecuteGovernanceProposalRequest{
		ProposalID:    "gov_01",
		Executor:      "0xfb6916095ca1df60bb749c63afe3ca9d2e281d5a",
		ChainID:       "falari-devnet",
		CreatedAtUnix: 1770000000,
	}
	executeHash, err := GovernanceExecuteHash(executeReq)
	if err != nil {
		t.Fatal(err)
	}
	const wantExecuteHash = "0x3847e7c1dbc4f9751ef1f1a66f54e67e03536a282a1cd25f7305711156ea93ae"
	if got := encodeHex(executeHash); !strings.EqualFold(got, wantExecuteHash) {
		t.Fatalf("execute signing hash changed:\n got %s\nwant %s", got, wantExecuteHash)
	}
	// The node normalises the executor address before hashing, so the client's
	// casing cannot change the digest — but the golden hash above is only
	// reproducible if the client normalises the same way.
	mixedCase := ExecuteGovernanceProposalRequest(executeReq)
	mixedCase.Executor = "0xfB6916095ca1df60bB749C63Afe3cA9d2E281D5A"
	mixedCaseHash, err := GovernanceExecuteHash(mixedCase)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mixedCaseHash, executeHash) {
		t.Fatal("executor address casing changes the execute signing hash")
	}
}

func governanceProposalPayloadKeyCount(req CreateGovernanceProposalRequest) (int, error) {
	data, err := GovernanceProposalPayload(req)
	if err != nil {
		return 0, err
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		return 0, err
	}
	return len(decoded), nil
}

func populatedGovernanceProposalRequest(t *testing.T, addr string) CreateGovernanceProposalRequest {
	t.Helper()
	req := CreateGovernanceProposalRequest{Proposer: addr, ChainID: "falari-test"}
	v := reflect.ValueOf(&req).Elem()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		switch field.Kind() {
		case reflect.String:
			if field.String() == "" {
				field.SetString("v" + strconv.Itoa(i))
			}
		case reflect.Bool:
			field.SetBool(true)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			field.SetInt(int64(1000 + i))
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			field.SetUint(uint64(1000 + i))
		case reflect.Slice:
			field.Set(reflect.Append(field, reflect.ValueOf("s0").Convert(field.Type().Elem())))
		default:
			t.Fatalf("populatedGovernanceProposalRequest: unhandled kind %s for field %s", field.Kind(), v.Type().Field(i).Name)
		}
	}
	return req
}

func signingPayloadJSONTags(payloadType reflect.Type) map[string]bool {
	tags := make(map[string]bool, payloadType.NumField())
	for i := 0; i < payloadType.NumField(); i++ {
		tags[jsonFieldTag(payloadType.Field(i))] = true
	}
	return tags
}

func jsonFieldTag(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if name := strings.Split(tag, ",")[0]; name != "" {
		return name
	}
	return field.Name
}

func tamperValue(field reflect.Value) {
	switch field.Kind() {
	case reflect.String:
		field.SetString(field.String() + "x")
	case reflect.Bool:
		field.SetBool(!field.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		field.SetInt(field.Int() + 7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		field.SetUint(field.Uint() + 7)
	case reflect.Slice:
		field.Set(reflect.Append(field, reflect.ValueOf("tampered").Convert(field.Type().Elem())))
	}
}
