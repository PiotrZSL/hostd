/*
	When changing the schema, the version must be incremented at the bottom of
	this file and a migration added to migrations.go
*/

CREATE TABLE wallet_utxos (
	id BLOB PRIMARY KEY,
	amount BLOB NOT NULL,
	unlock_hash BLOB NOT NULL
);

CREATE TABLE wallet_transactions (
	id BLOB PRIMARY KEY,
	source TEXT NOT NULL,
	block_id BLOB NOT NULL,
	inflow BLOB NOT NULL,
	outflow BLOB NOT NULL,
	block_height INTEGER NOT NULL,
	block_index INTEGER NOT NULL,
	raw_data BLOB NOT NULL, -- binary serialized transaction
	date_created INTEGER NOT NULL,
	UNIQUE(block_height, block_index)
);
CREATE INDEX wallet_transactions_date_created_index ON wallet_transactions(date_created);

CREATE TABLE accounts (
	id BLOB PRIMARY KEY,
	balance BLOB NOT NULL,
	expiration_height INTEGER NOT NULL
);

CREATE TABLE storage_volumes (
	id INTEGER PRIMARY KEY,
	disk_path TEXT UNIQUE NOT NULL,
	read_only BOOLEAN NOT NULL,
	available BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE volume_sectors (
	id INTEGER PRIMARY KEY,
	volume_id INTEGER NOT NULL REFERENCES storage_volumes, -- all sectors will need to be migrated first when deleting a volume
	volume_index INTEGER NOT NULL,
	sector_root BLOB UNIQUE, -- set null if the sector is not used
	UNIQUE (volume_id, volume_index)
);
CREATE INDEX volume_sectors_volume_id ON volume_sectors(volume_id, volume_index);

CREATE TABLE locked_volume_sectors ( -- should be cleared at startup. currently persisted for simplicity, but may be moved to memory
	id INTEGER PRIMARY KEY,
	volume_sector_id INTEGER REFERENCES volume_sectors(id) ON DELETE CASCADE
);

CREATE TABLE contracts (
	id BLOB PRIMARY KEY,
	renewed_from BLOB REFERENCES contracts ON DELETE SET NULL,
	renewed_to BLOB REFERENCES contracts ON DELETE SET NULL,
	locked_collateral BLOB NOT NULL,
	contract_error TEXT,
	revision_number BLOB NOT NULL, -- stored as BLOB to support uint64_max on clearing revisions
	confirmed_revision_number BLOB DEFAULT '0', -- determines if the final revision should be broadcast; stored as BLOB to support uint64_max on clearing revisions
	formation_confirmed BOOLEAN NOT NULL DEFAULT false, -- true if the contract has been confirmed on the blockchain
	resolution_confirmed BOOLEAN NOT NULL DEFAULT false, -- true if the storage proof/resolution has been confirmed on the blockchain
	negotiation_height INTEGER NOT NULL, -- determines if the formation txn should be rebroadcast or if the contract should be deleted
	window_start INTEGER NOT NULL,
	window_end INTEGER NOT NULL,
	formation_txn_set BLOB NOT NULL, -- binary serialized transaction set
	raw_revision BLOB NOT NULL, -- binary serialized contract revision
	host_sig BLOB NOT NULL,
	renter_sig BLOB NOT NULL
);
CREATE INDEX contracts_window_start_index ON contracts(window_start);
CREATE INDEX contracts_window_end_index ON contracts(window_end);

CREATE TABLE contract_sector_roots (
	id INTEGER PRIMARY KEY,
	contract_id BLOB REFERENCES contracts(id) ON DELETE CASCADE,
	sector_root BLOB NOT NULL,
	root_index INTEGER NOT NULL,
	UNIQUE(contract_id, root_index)
);
CREATE INDEX contract_sector_roots_contract_id_root_index ON contract_sector_roots(contract_id, root_index);
CREATE INDEX contract_sector_roots_sector_root ON contract_sector_roots(sector_root);

CREATE TABLE temp_storage (
	sector_root BLOB PRIMARY KEY,
	expiration_height INTEGER NOT NULL
);

CREATE TABLE financial_account_funding (
	source BLOB NOT NULL,
	destination BLOB NOT NULL,
	amount BLOB NOT NULL,
	reverted BOOLEAN NOT NULL,
	date_created INTEGER NOT NULL
);
CREATE INDEX financial_account_funding_source ON financial_account_funding(source);
CREATE INDEX financial_account_funding_reverted ON financial_account_funding(reverted);
CREATE INDEX financial_account_funding_date_created ON financial_account_funding(date_created);

CREATE TABLE financial_records (
	source_id BLOB NOT NULL,
	egress_revenue BLOB NOT NULL,
	ingress_revenue BLOB NOT NULL,
	storage_revenue BLOB NOT NULL,
	fee_revenue BLOB NOT NULL,
	reverted BOOLEAN NOT NULL,
	date_created INTEGER NOT NULL
);
CREATE INDEX financial_records_source_id ON financial_records(source_id);
CREATE INDEX financial_records_date_created ON financial_records(date_created);

CREATE TABLE host_settings (
	id INT PRIMARY KEY NOT NULL DEFAULT 0 CHECK (id = 0), -- enforce a single row
	settings_revision INTEGER NOT NULL,
	accepting_contracts BOOLEAN NOT NULL,
	net_address TEXT NOT NULL,
	contract_price BLOB NOT NULL,
	base_rpc_price BLOB NOT NULL,
	sector_access_price BLOB NOT NULL,
	collateral BLOB NOT NULL,
	max_collateral BLOB NOT NULL,
	min_storage_price BLOB NOT NULL,
	min_egress_price BLOB NOT NULL,
	min_ingress_price BLOB NOT NULL,
	max_account_balance BLOB NOT NULL,
	max_account_age INTEGER NOT NULL,
	max_contract_duration INTEGER NOT NULL,
	ingress_limit INTEGER NOT NULL,
	egress_limit INTEGER NOT NULL,
	last_processed_consensus_change BLOB NOT NULL
);

CREATE TABLE global_settings (
	id INT PRIMARY KEY NOT NULL DEFAULT 0 CHECK (id = 0), -- enforce a single row
	db_version INTEGER NOT NULL DEFAULT 0, -- used for migrations
	host_key BLOB NOT NULL DEFAULT "", -- host key will eventually be stored instead of passed into the CLI, this will make migrating from siad easier
	host_last_processed_change BLOB NOT NULL DEFAULT "", -- last processed consensus change for the host
	wallet_last_processed_change BLOB NOT NULL DEFAULT "", -- last processed consensus change for the wallet
	contracts_last_processed_change BLOB NOT NULL DEFAULT "" -- last processed consensus change for the contract manager
);

INSERT INTO global_settings (db_version) VALUES (1); -- version must be updated when the schema changes