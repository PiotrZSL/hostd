package contracts

import (
	"encoding/binary"
	"fmt"
	"math/bits"

	"go.sia.tech/hostd/host/storage"
	"go.sia.tech/hostd/internal/merkle"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/types"
	"golang.org/x/crypto/blake2b"
)

// An action determines what lifecycle event should be performed on a contract.
const (
	ActionBroadcastFormation     LifecycleAction = "formation"
	ActionBroadcastFinalRevision LifecycleAction = "revision"
	ActionBroadcastResolution    LifecycleAction = "resolution"
)

type (
	// LifecycleAction is an action that should be performed on a contract.
	LifecycleAction string
)

// storageProofSegment returns the segment index for which a storage proof must
// be provided, given a contract and the block at the beginning of its proof
// window.
func storageProofSegment(bid types.BlockID, fcid types.FileContractID, filesize uint64) uint64 {
	if filesize == 0 {
		return 0
	}
	seed := blake2b.Sum256(append(bid[:], fcid[:]...))
	numSegments := filesize / merkle.LeafSize
	if filesize%merkle.LeafSize != 0 {
		numSegments++
	}
	var r uint64
	for i := 0; i < 4; i++ {
		_, r = bits.Div64(r, binary.BigEndian.Uint64(seed[i*8:]), numSegments)
	}
	return r
}

func (cm *ContractManager) buildStorageProof(id types.FileContractID, index uint64) (types.StorageProof, error) {
	sectorIndex := index / merkle.LeavesPerSector
	segmentIndex := index % merkle.LeavesPerSector

	roots, err := cm.SectorRoots(id, 0, 0)
	if err != nil {
		return types.StorageProof{}, err
	}
	root := roots[sectorIndex]
	sector, err := cm.storage.Read(storage.SectorRoot(root))
	if err != nil {
		return types.StorageProof{}, err
	}
	segmentProof := merkle.ConvertProofOrdering(merkle.BuildProof(sector, segmentIndex, segmentIndex+1, nil), segmentIndex)
	sectorProof := merkle.ConvertProofOrdering(merkle.BuildSectorRangeProof(roots, sectorIndex, sectorIndex+1), sectorIndex)
	sp := types.StorageProof{
		ParentID: id,
		HashSet:  append(segmentProof, sectorProof...),
	}
	copy(sp.Segment[:], sector[segmentIndex*merkle.LeafSize:])
	return sp, nil
}

// handleContractAction performs a lifecycle action on a contract.
func (cm *ContractManager) handleContractAction(id types.FileContractID, action LifecycleAction) error {
	contract, err := cm.store.Contract(id)
	if err != nil {
		return fmt.Errorf("failed to get contract: %w", err)
	}

	switch action {
	case ActionBroadcastFormation:
		formationSet, err := cm.store.ContractFormationSet(id)
		if err != nil {
			return fmt.Errorf("failed to get formation set: %w", err)
		} else if err := cm.tpool.AcceptTransactionSet(formationSet); err != nil {
			// TODO: recalc financials
			return fmt.Errorf("failed to broadcast formation txn: %w", err)
		}
	case ActionBroadcastFinalRevision:
		revisionTxn := types.Transaction{
			FileContractRevisions: []types.FileContractRevision{contract.Revision},
			TransactionSignatures: []types.TransactionSignature{
				{
					ParentID:      crypto.Hash(contract.Revision.ParentID),
					CoveredFields: types.CoveredFields{FileContractRevisions: []uint64{0}},
					Signature:     contract.RenterSignature[:],
				},
				{
					ParentID:      crypto.Hash(contract.Revision.ParentID),
					CoveredFields: types.CoveredFields{FileContractRevisions: []uint64{0}},
					Signature:     contract.HostSignature[:],
				},
			},
		}

		_, max := cm.tpool.FeeEstimation()
		fee := max.Mul64(1000)
		revisionTxn.MinerFees = append(revisionTxn.MinerFees, fee)
		toSign, discard, err := cm.wallet.FundTransaction(&revisionTxn, fee)
		if err != nil {
			return fmt.Errorf("failed to fund revision txn: %w", err)
		}
		defer discard()
		if err := cm.wallet.SignTransaction(&revisionTxn, toSign, types.FullCoveredFields); err != nil {
			return fmt.Errorf("failed to sign revision txn: %w", err)
		} else if err := cm.tpool.AcceptTransactionSet([]types.Transaction{revisionTxn}); err != nil {
			return fmt.Errorf("failed to broadcast revision txn: %w", err)
		}
	case ActionBroadcastResolution:
		state, err := cm.chain.IndexAtHeight(uint64(contract.Revision.NewWindowStart - 1))
		if err != nil {
			return fmt.Errorf("failed to get chain index at height %v: %w", contract.Revision.NewWindowStart-1, err)
		}
		index := storageProofSegment(state.ID, contract.Revision.ParentID, contract.Revision.NewFileSize)
		sp, err := cm.buildStorageProof(contract.Revision.ParentID, index)
		if err != nil {
			return fmt.Errorf("failed to build storage proof: %w", err)
		}

		// TODO: consider cost of proof submission and build proof.
		resolutionTxn := types.Transaction{
			StorageProofs: []types.StorageProof{sp},
		}
		_, max := cm.tpool.FeeEstimation()
		fee := max.Mul64(1000)
		resolutionTxn.MinerFees = append(resolutionTxn.MinerFees, fee)
		toSign, discard, err := cm.wallet.FundTransaction(&resolutionTxn, fee)
		if err != nil {
			return fmt.Errorf("failed to fund resolution txn: %w", err)
		}
		defer discard()
		if err := cm.wallet.SignTransaction(&resolutionTxn, toSign, types.FullCoveredFields); err != nil {
			return fmt.Errorf("failed to sign resolution txn: %w", err)
		} else if err := cm.tpool.AcceptTransactionSet([]types.Transaction{resolutionTxn}); err != nil {
			return fmt.Errorf("failed to broadcast resolution txn: %w", err)
		}
	}
	return nil
}
