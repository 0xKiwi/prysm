package testutil

import (
	"context"
	"math"
	"math/rand"
	"testing"

	"github.com/golang/protobuf/proto"
	"github.com/prysmaticlabs/go-bitfield"
	"github.com/prysmaticlabs/go-ssz"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/blocks"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/helpers"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/state"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	ethpb "github.com/prysmaticlabs/prysm/proto/eth/v1alpha1"
	"github.com/prysmaticlabs/prysm/shared/bls"
	"github.com/prysmaticlabs/prysm/shared/params"
)

// BlockGenConfig is used to define the requested conditions
// for block generation.
type BlockGenConfig struct {
	MaxProposerSlashings uint64
	MaxAttesterSlashings uint64
	MaxAttestations      uint64
	MaxDeposits          uint64
	MaxVoluntaryExits    uint64
	Signatures           bool
}

// DefaultBlockGenConfig returns the block config that utilizes the
// current params in the beacon config.
func DefaultBlockGenConfig() *BlockGenConfig {
	return &BlockGenConfig{
		MaxProposerSlashings: params.BeaconConfig().MaxProposerSlashings,
		MaxAttesterSlashings: params.BeaconConfig().MaxAttesterSlashings,
		MaxAttestations:      params.BeaconConfig().MaxAttestations,
		MaxDeposits:          params.BeaconConfig().MaxDeposits,
		MaxVoluntaryExits:    params.BeaconConfig().MaxVoluntaryExits,
	}
}

// GenerateFullBlock generates a fully valid block with the requested parameters.
// Use BlockGenConfig to declare the conditions you would like the block generated under.
func GenerateFullBlock(
	t testing.TB,
	bState *pb.BeaconState,
	privs []*bls.SecretKey,
	conf *BlockGenConfig,
	slot uint64,
) *ethpb.BeaconBlock {

	currentSlot := bState.Slot
	if currentSlot > slot {
		t.Fatalf("Current slot in state is larger than given slot. %d > %d", currentSlot, slot)
	}

	pSlashings := []*ethpb.ProposerSlashing{}
	if conf.MaxProposerSlashings > 0 {
		pSlashings = generateProposerSlashings(t, bState, privs, conf.MaxProposerSlashings)
	}

	aSlashings := []*ethpb.AttesterSlashing{}
	if conf.MaxAttesterSlashings > 0 {
		aSlashings = generateAttesterSlashings(t, bState, privs, conf.MaxAttesterSlashings)
	}

	atts := []*ethpb.Attestation{}
	if conf.MaxAttestations > 0 {
		atts = GenerateAttestations(t, bState, privs, conf)
	}

	newDeposits, eth1Data := []*ethpb.Deposit{}, bState.Eth1Data
	if conf.MaxDeposits > 0 {
		newDeposits, eth1Data = generateDepositsAndEth1Data(t, bState, conf.MaxDeposits)
	}

	exits := []*ethpb.VoluntaryExit{}
	if conf.MaxVoluntaryExits > 0 {
		exits = generateVoluntaryExits(t, bState, privs, conf.MaxVoluntaryExits)
	}

	newHeader := proto.Clone(bState.LatestBlockHeader).(*ethpb.BeaconBlockHeader)
	prevStateRoot, err := ssz.HashTreeRoot(bState)
	if err != nil {
		t.Fatal(err)
	}
	newHeader.StateRoot = prevStateRoot[:]
	parentRoot, err := ssz.SigningRoot(newHeader)
	if err != nil {
		t.Fatal(err)
	}

	reveal := []byte{1, 2, 3, 4}
	if conf.Signatures {
		// Temporarily incrementing the beacon state slot here since BeaconProposerIndex is a
		// function deterministic on beacon state slot.
		bState.Slot = slot
		reveal, err = CreateRandaoReveal(bState, helpers.CurrentEpoch(bState), privs)
		if err != nil {
			t.Fatal(err)
		}
		bState.Slot = currentSlot
	}

	block := &ethpb.BeaconBlock{
		Slot:       slot,
		ParentRoot: parentRoot[:],
		Body: &ethpb.BeaconBlockBody{
			Eth1Data:          eth1Data,
			RandaoReveal:      reveal,
			ProposerSlashings: pSlashings,
			AttesterSlashings: aSlashings,
			Attestations:      atts,
			VoluntaryExits:    exits,
			Deposits:          newDeposits,
		},
	}

	s, err := state.CalculateStateRoot(context.Background(), bState, block)
	if err != nil {
		t.Fatal(err)
	}
	root, err := ssz.HashTreeRoot(s)
	if err != nil {
		t.Fatal(err)
	}
	block.StateRoot = root[:]

	if conf.Signatures {
		blockRoot, err := ssz.SigningRoot(block)
		if err != nil {
			t.Fatal(err)
		}
		// Temporarily incrementing the beacon state slot here since BeaconProposerIndex is a
		// function deterministic on beacon state slot.
		bState.Slot = slot
		proposerIdx, err := helpers.BeaconProposerIndex(bState)
		if err != nil {
			t.Fatal(err)
		}
		bState.Slot = currentSlot
		domain := helpers.Domain(bState.Fork, helpers.CurrentEpoch(bState), params.BeaconConfig().DomainBeaconProposer)
		block.Signature = privs[proposerIdx].Sign(blockRoot[:], domain).Marshal()
	}

	return block
}

func generateProposerSlashings(
	t testing.TB,
	bState *pb.BeaconState,
	privs []*bls.SecretKey,
	numSlashings uint64,
) []*ethpb.ProposerSlashing {
	currentEpoch := helpers.CurrentEpoch(bState)
	slotsPerEpoch := params.BeaconConfig().SlotsPerEpoch

	proposerSlashings := make([]*ethpb.ProposerSlashing, numSlashings)
	for i := uint64(0); i < numSlashings; i++ {
		proposerIndex, err := randValIndex(bState)
		if err != nil {
			t.Fatal(err)
		}
		header1 := &ethpb.BeaconBlockHeader{
			Slot:     bState.Slot,
			BodyRoot: []byte{0, 1, 0},
		}
		root, err := ssz.SigningRoot(header1)
		if err != nil {
			t.Fatal(err)
		}
		domain := helpers.Domain(bState.Fork, currentEpoch, params.BeaconConfig().DomainBeaconProposer)
		header1.Signature = privs[proposerIndex].Sign(root[:], domain).Marshal()

		header2 := &ethpb.BeaconBlockHeader{
			Slot:     bState.Slot,
			BodyRoot: []byte{0, 2, 0},
		}
		root, err = ssz.SigningRoot(header2)
		if err != nil {
			t.Fatal(err)
		}
		header2.Signature = privs[proposerIndex].Sign(root[:], domain).Marshal()

		slashing := &ethpb.ProposerSlashing{
			ProposerIndex: proposerIndex,
			Header_1:      header1,
			Header_2:      header2,
		}
		proposerSlashings[i] = slashing
	}
	return proposerSlashings
}

func generateAttesterSlashings(
	t testing.TB,
	bState *pb.BeaconState,
	privs []*bls.SecretKey,
	numSlashings uint64,
) []*ethpb.AttesterSlashing {
	attesterSlashings := make([]*ethpb.AttesterSlashing, numSlashings)
	for i := uint64(0); i < numSlashings; i++ {
		committee, err := helpers.BeaconCommittee(bState, bState.Slot, rand.Uint64()%params.BeaconConfig().MaxCommitteesPerSlot)
		if err != nil {
			t.Fatal(err)
		}
		committeeSize := uint64(len(committee))

		aggregationBits := bitfield.NewBitlist(committeeSize)
		aggregationBits.SetBitAt(i, true)
		att1 := &ethpb.Attestation{
			Data: &ethpb.AttestationData{
				Target: &ethpb.Checkpoint{
					Epoch: i,
					Root:  params.BeaconConfig().ZeroHash[:],
				},
				Source: &ethpb.Checkpoint{
					Epoch: i + 1,
					Root:  params.BeaconConfig().ZeroHash[:],
				},
			},
			AggregationBits: aggregationBits,
		}
		dataRoot, err := ssz.HashTreeRoot(&pb.AttestationDataAndCustodyBit{
			Data:       att1.Data,
			CustodyBit: false,
		})
		if err != nil {
			t.Fatal(err)
		}
		domain := helpers.Domain(bState.Fork, i, params.BeaconConfig().DomainBeaconAttester)
		valIndex := committee[rand.Uint64()%uint64(len(committee))]
		sig := privs[valIndex].Sign(dataRoot[:], domain)
		att1.Signature = bls.AggregateSignatures([]*bls.Signature{sig}).Marshal()

		att2 := &ethpb.Attestation{
			Data: &ethpb.AttestationData{
				Target: &ethpb.Checkpoint{
					Epoch: i,
					Root:  params.BeaconConfig().ZeroHash[:],
				},
				Source: &ethpb.Checkpoint{
					Epoch: i,
					Root:  params.BeaconConfig().ZeroHash[:],
				},
			},
			AggregationBits: aggregationBits,
		}
		dataRoot, err = ssz.HashTreeRoot(&pb.AttestationDataAndCustodyBit{
			Data:       att2.Data,
			CustodyBit: false,
		})
		if err != nil {
			t.Fatal(err)
		}
		sig = privs[valIndex].Sign(dataRoot[:], domain)
		att2.Signature = bls.AggregateSignatures([]*bls.Signature{sig}).Marshal()

		indexedAtt1, err := blocks.ConvertToIndexed(context.Background(), bState, att1)
		if err != nil {
			t.Fatal(err)
		}
		indexedAtt2, err := blocks.ConvertToIndexed(context.Background(), bState, att2)
		if err != nil {
			t.Fatal(err)
		}
		slashing := &ethpb.AttesterSlashing{
			Attestation_1: indexedAtt1,
			Attestation_2: indexedAtt2,
		}
		attesterSlashings[i] = slashing
	}
	return attesterSlashings
}

// GenerateAttestations creates attestations that are entirely valid, for all the committees of the current state slot.
// This function always returns all validators participating.
// Attestations requested must be cleanly divisible by committees per slot. then it will
// return 1 attestation with all validators aggregated into it. If maxAttestations is set to 4, then
// it will return 4 attestations for the same data with their aggregation bits split uniformly.
func GenerateAttestations(
	t testing.TB,
	bState *pb.BeaconState,
	privs []*bls.SecretKey,
	conf *BlockGenConfig,
) []*ethpb.Attestation {
	maxAttestations := conf.MaxAttestations
	currentEpoch := helpers.CurrentEpoch(bState)
	attestations := make([]*ethpb.Attestation, maxAttestations)

	var err error
	targetRoot := make([]byte, 32)
	headRoot := make([]byte, 32)
	epochStartSlot := helpers.StartSlot(currentEpoch)
	// Only calculate head state if its needed for boundary.
	if bState.Slot+1 == helpers.StartSlot(currentEpoch+1) {
		headState := proto.Clone(bState).(*pb.BeaconState)
		headState, err := state.ProcessSlots(context.Background(), headState, bState.Slot+1)
		if err != nil {
			t.Fatal(err)
		}
		headRoot, err = helpers.BlockRootAtSlot(headState, bState.Slot)
		if err != nil {
			t.Fatal(err)
		}
		targetRoot = headRoot
	} else {
		targetRoot, err = helpers.BlockRootAtSlot(bState, epochStartSlot)
		if err != nil {
			t.Fatal(err)
		}
	}

	committeesPerSlot, err := helpers.CommitteeCountAtSlot(bState, bState.Slot)
	if err != nil {
		t.Fatal(err)
	}

	if maxAttestations < committeesPerSlot {
		t.Logf(
			"Warning: %d attestations requested is less than %d committees in current slot, not all validators will be attesting.",
			maxAttestations,
			committeesPerSlot,
		)
	}

	attsPerCommittee := math.Min(float64(maxAttestations/committeesPerSlot), 1)
	if math.Trunc(attsPerCommittee) == attsPerCommittee {
		t.Fatalf("requested attestations must be easily divisible, ")
	}

	domain := helpers.Domain(bState.Fork, currentEpoch, params.BeaconConfig().DomainBeaconAttester)
	for c := uint64(0); c < committeesPerSlot || c < maxAttestations; c++ {
		committee, err := helpers.BeaconCommittee(bState, currentEpoch, c)
		if err != nil {
			t.Fatal(err)
		}
		committeeSize := uint64(len(committee))
		bitsPerAtt := committeeSize / uint64(attsPerCommittee)
		for i := uint64(0); i < committeeSize; i += bitsPerAtt {
			aggregationBits := bitfield.NewBitlist(committeeSize)
			sigs := []*bls.Signature{}
			for b := i; b < i+bitsPerAtt; b++ {
				aggregationBits.SetBitAt(b, true)
				sigs = append(sigs, privs[committee[b]].Sign(dataRoot[:], domain))
			}

			attestations[i/bitsPerAtt*(c+1)] = &ethpb.Attestation{
				Data: &ethpb.AttestationData{
					Slot:            bState.Slot,
					Index:           c,
					BeaconBlockRoot: headRoot,
					Source:          bState.CurrentJustifiedCheckpoint,
					Target: &ethpb.Checkpoint{
						Epoch: currentEpoch,
						Root:  targetRoot,
					},
				},
				AggregationBits: aggregationBits,
				Signature:       bls.AggregateSignatures(sigs).Marshal(),
			}
		}
	}
	return attestations
}

func generateDepositsAndEth1Data(
	t testing.TB,
	bState *pb.BeaconState,
	numDeposits uint64,
) (
	[]*ethpb.Deposit,
	*ethpb.Eth1Data,
) {
	previousDepsLen := bState.Eth1DepositIndex
	currentDeposits, _, _ := SetupInitialDeposits(t, previousDepsLen+numDeposits)
	eth1Data := GenerateEth1Data(t, currentDeposits)
	return currentDeposits[previousDepsLen:], eth1Data
}

func generateVoluntaryExits(
	t testing.TB,
	bState *pb.BeaconState,
	privs []*bls.SecretKey,
	numExits uint64,
) []*ethpb.VoluntaryExit {
	currentEpoch := helpers.CurrentEpoch(bState)
	validatorCount, err := helpers.ActiveValidatorCount(bState, currentEpoch)
	if err != nil {
		t.Fatal(err)
	}

	voluntaryExits := make([]*ethpb.VoluntaryExit, numExits)
	for i := 0; i < len(voluntaryExits); i++ {
		valIndex, err := randValIndex(bState)
		if err != nil {
			t.Fatal(err)
		}
		exit := &ethpb.VoluntaryExit{
			Epoch:          helpers.PrevEpoch(bState),
			ValidatorIndex: valIndex,
		}
		root, err := ssz.SigningRoot(exit)
		if err != nil {
			t.Fatal(err)
		}
		domain := helpers.Domain(bState.Fork, currentEpoch, params.BeaconConfig().DomainVoluntaryExit)
		exit.Signature = privs[valIndex].Sign(root[:], domain).Marshal()
		voluntaryExits[i] = exit
	}
	return voluntaryExits
}

func randValIndex(bState *pb.BeaconState) (uint64, error) {
	activeCount, err := helpers.ActiveValidatorCount(bState, helpers.CurrentEpoch(bState))
	if err != nil {
		return 0, err
	}
	return rand.Uint64() % activeCount, nil
}
