package sqlite

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"go.sia.tech/core/types"
	"go.sia.tech/hostd/wallet"
	"go.sia.tech/siad/modules"
)

func encodeTransaction(txn wallet.Transaction) []byte {
	var buf bytes.Buffer
	e := types.NewEncoder(&buf)
	txn.EncodeTo(e)
	e.Flush()
	return buf.Bytes()
}

func decodeTransaction(b []byte, txn *wallet.Transaction) error {
	d := types.NewBufDecoder(b)
	txn.DecodeFrom(d)
	return d.Err()
}

// An updateWalletTxn atomically updates the wallet
type updateWalletTxn struct {
	tx txn
}

// AddSiacoinElement adds a spendable siacoin output to the wallet.
func (tx *updateWalletTxn) AddSiacoinElement(utxo wallet.SiacoinElement) error {
	_, err := tx.tx.Exec(`INSERT INTO wallet_utxos (id, amount, unlock_hash) VALUES (?, ?, ?)`, sqlHash256(utxo.ID), sqlCurrency(utxo.Value), sqlHash256(utxo.Address))
	return err
}

// RemoveSiacoinElement removes a spendable siacoin output from the wallet
// either due to a spend or a reorg.
func (tx *updateWalletTxn) RemoveSiacoinElement(id types.SiacoinOutputID) error {
	_, err := tx.tx.Exec(`DELETE FROM wallet_utxos WHERE id=?`, sqlHash256(id))
	return err
}

// AddTransaction adds a transaction to the wallet.
func (tx *updateWalletTxn) AddTransaction(txn wallet.Transaction, idx uint64) error {
	const query = `INSERT INTO wallet_transactions (id, block_id, block_height, block_index, source, inflow, outflow, raw_data, date_created) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := tx.tx.Exec(query,
		sqlHash256(txn.ID),
		sqlHash256(txn.Index.ID),
		txn.Index.Height,
		idx,
		txn.Source,
		sqlCurrency(txn.Inflow),
		sqlCurrency(txn.Outflow),
		encodeTransaction(txn),
		sqlTime(txn.Timestamp),
	)
	return err
}

// RemoveTransaction removes a transaction from the wallet.
func (tx *updateWalletTxn) RemoveTransaction(id types.TransactionID) error {
	_, err := tx.tx.Exec(`DELETE FROM wallet_transactions WHERE id=?`, sqlHash256(id))
	return err
}

// SetLastChange sets the last processed consensus change.
func (tx *updateWalletTxn) SetLastChange(id modules.ConsensusChangeID) error {
	_, err := tx.tx.Exec(`INSERT INTO global_settings (wallet_last_processed_change) VALUES(?) ON CONFLICT (ID) DO UPDATE SET wallet_last_processed_change=excluded.wallet_last_processed_change`, hex.EncodeToString(id[:]))
	return err
}

// LastWalletChange gets the last consensus change processed by the wallet.
func (s *Store) LastWalletChange() (id modules.ConsensusChangeID, err error) {
	var consensusID string
	err = s.db.QueryRow(`SELECT wallet_last_processed_change FROM global_settings`).Scan(&consensusID)
	if errors.Is(err, sql.ErrNoRows) {
		return modules.ConsensusChangeBeginning, nil
	} else if err != nil {
		return modules.ConsensusChangeBeginning, fmt.Errorf("failed to query last wallet change: %w", err)
	} else if len(consensusID) == 0 {
		return modules.ConsensusChangeBeginning, nil
	}
	n, err := hex.Decode(id[:], []byte(consensusID))
	if err != nil {
		return modules.ConsensusChangeBeginning, fmt.Errorf("failed to decode last wallet change: %w", err)
	} else if n != len(id) {
		return modules.ConsensusChangeBeginning, fmt.Errorf("failed to decode last wallet change: expected %d bytes, got %d", len(id), n)
	}
	return
}

// UnspentSiacoinElements returns the spendable siacoin outputs in the wallet.
func (s *Store) UnspentSiacoinElements() (utxos []wallet.SiacoinElement, err error) {
	rows, err := s.db.Query(`SELECT id, amount, unlock_hash FROM wallet_utxos`)
	if err != nil {
		return nil, fmt.Errorf("failed to query unspent siacoin elements: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var utxo wallet.SiacoinElement
		if err := rows.Scan((*sqlHash256)(&utxo.ID), (*sqlCurrency)(&utxo.Value), (*sqlHash256)(&utxo.Address)); err != nil {
			return nil, fmt.Errorf("failed to scan unspent siacoin element: %w", err)
		}
		utxos = append(utxos, utxo)
	}
	return utxos, nil
}

// Transactions returns a paginated list of transactions ordered by block height
// descending. If no transactions are found, (nil, nil) is returned.
func (s *Store) Transactions(limit, offset int) (txns []wallet.Transaction, err error) {
	rows, err := s.db.Query(`SELECT id, block_id, block_height, source, inflow, outflow, raw_data, date_created FROM wallet_transactions ORDER BY block_height DESC, block_index ASC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query transactions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var txn wallet.Transaction
		var buf []byte
		if err := rows.Scan((*sqlHash256)(&txn.ID), (*sqlHash256)(&txn.Index.ID), &txn.Index.Height, &txn.Source, (*sqlCurrency)(&txn.Inflow), (*sqlCurrency)(&txn.Outflow), &buf, (*sqlTime)(&txn.Timestamp)); err != nil {
			return nil, fmt.Errorf("failed to scan transaction: %w", err)
		} else if err := decodeTransaction(buf, &txn); err != nil {
			return nil, fmt.Errorf("failed to unmarshal transaction data: %w", err)
		}
		txns = append(txns, txn)
	}
	return
}

// TransactionCount returns the total number of transactions in the wallet.
func (s *Store) TransactionCount() (count uint64, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*) FROM wallet_transactions`).Scan(&count)
	return
}

// UpdateWallet begins an update transaction on the wallet store.
func (s *Store) UpdateWallet(fn func(wallet.UpdateTransaction) error) error {
	return s.transaction(func(tx txn) error {
		return fn(&updateWalletTxn{tx})
	})
}