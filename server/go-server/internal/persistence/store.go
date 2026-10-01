package persistence

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/scrypt"
	"golang.org/x/text/encoding/simplifiedchinese"
	"kungfu.local/server/internal/protocol"
	"regexp"
	"strings"
	"time"
)

var ErrDenied = errors.New("request rejected")
var accountPattern = regexp.MustCompile(`^[a-zA-Z0-9]{3,20}$`)
var legacyPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

type Store struct {
	DB           *sql.DB
	wordFilter   *wordCache
	rankings     [13]rankingSlot
	rankingRedis *rankingRedis
}
type Account struct {
	BanGeneration uint64   `json:"-"`
	UID           uint64   `json:"uid"`
	Account       string   `json:"account"`
	Nickname      string   `json:"nickname"`
	Profile       []byte   `json:"profile"`
	Salt          []byte   `json:"salt,omitempty"`
	Digest        []byte   `json:"digest,omitempty"`
	LegacySalt    []byte   `json:"legacy_salt,omitempty"`
	LegacyDigest  []byte   `json:"legacy_digest,omitempty"`
	Gold          uint32   `json:"gold"`
	Tickets       uint32   `json:"tickets"`
	Inventory     [][]byte `json:"inventory,omitempty"`
}
type Offer struct {
	Key      uint32 `json:"key"`
	Category byte   `json:"category"`
	Variant  byte   `json:"variant"`
	Record   []byte `json:"record"`
	Grant    []byte `json:"grant"`
}
type Export struct {
	Accounts []Account `json:"accounts"`
	Offers   []Offer   `json:"offers"`
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS treasure_draws(uid BIGINT UNSIGNED NOT NULL, operation_id VARCHAR(100) CHARACTER SET ascii NOT NULL, request_hash BINARY(32) NOT NULL, snapshot LONGBLOB NOT NULL, result LONGBLOB NOT NULL, created TIMESTAMP DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY(uid,operation_id)) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS gm_notices(id VARCHAR(100) PRIMARY KEY, content VARCHAR(200) NOT NULL, state VARCHAR(20) NOT NULL, recipients INT NOT NULL DEFAULT 0, created BIGINT NOT NULL, INDEX(state,created)) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS banned_words_config(id TINYINT PRIMARY KEY, revision BIGINT UNSIGNED NOT NULL, words JSON NOT NULL)`,
	// accounts precedes every FK-dependent table; stage_player_unlocks once
	// referenced it before creation and fresh databases failed with error 1824.
	`CREATE TABLE IF NOT EXISTS accounts(
        uid BIGINT UNSIGNED PRIMARY KEY ,
        account VARCHAR(20) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE ,
        nickname VARCHAR(40) NOT NULL ,
        profile VARBINARY(360) NOT NULL ,
        salt VARBINARY(16) NOT NULL ,
        digest VARBINARY(32) NOT NULL ,
        legacy_salt VARBINARY(16) NOT NULL ,
        legacy_digest VARBINARY(32) NOT NULL ,
        gold BIGINT UNSIGNED NOT NULL DEFAULT 0 ,
        tickets BIGINT UNSIGNED NOT NULL DEFAULT 0
    ) ENGINE=InnoDB`,
	accountBanSchema,
	accountBanAuditSchema,
	`CREATE TABLE IF NOT EXISTS random_weapon_settings(uid BIGINT UNSIGNED PRIMARY KEY, mode TINYINT UNSIGNED NOT NULL DEFAULT 0, instance INT UNSIGNED NOT NULL DEFAULT 0, FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS honour_rules(id TINYINT UNSIGNED PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,rules MEDIUMBLOB NOT NULL) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS honour_rules_audit(revision BIGINT UNSIGNED PRIMARY KEY,before_data MEDIUMBLOB NOT NULL,after_data MEDIUMBLOB NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS battle_reward_rules(id TINYINT UNSIGNED PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,rules MEDIUMBLOB NOT NULL) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS training_rules(id TINYINT UNSIGNED PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,rules MEDIUMBLOB NOT NULL) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS weapon_rules(id TINYINT UNSIGNED PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,rules MEDIUMBLOB NOT NULL) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS weapon_rules_audit(revision BIGINT UNSIGNED PRIMARY KEY,before_data MEDIUMBLOB NOT NULL,after_data MEDIUMBLOB NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS talisman_rules(id TINYINT UNSIGNED PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,rules MEDIUMBLOB NOT NULL) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS talisman_rules_audit(revision BIGINT UNSIGNED PRIMARY KEY,before_data MEDIUMBLOB NOT NULL,after_data MEDIUMBLOB NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS task_rules(id TINYINT UNSIGNED PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,rules MEDIUMBLOB NOT NULL) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS title_rules(id TINYINT UNSIGNED PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,rules MEDIUMBLOB NOT NULL) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS title_rules_audit(revision BIGINT UNSIGNED PRIMARY KEY,before_data MEDIUMBLOB NOT NULL,after_data MEDIUMBLOB NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS task_rules_audit(revision BIGINT UNSIGNED PRIMARY KEY,before_data MEDIUMBLOB NOT NULL,after_data MEDIUMBLOB NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS training_rules_audit(revision BIGINT UNSIGNED PRIMARY KEY,before_data MEDIUMBLOB NOT NULL,after_data MEDIUMBLOB NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS stage_access(id TINYINT UNSIGNED PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,rules MEDIUMBLOB NOT NULL) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS stage_player_unlocks(uid BIGINT UNSIGNED NOT NULL,client_hash CHAR(64) CHARACTER SET ascii NOT NULL,revision BIGINT UNSIGNED NOT NULL,maps MEDIUMBLOB NOT NULL,PRIMARY KEY(uid,client_hash),FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS stage_player_unlock_audit(uid BIGINT UNSIGNED NOT NULL,client_hash CHAR(64) CHARACTER SET ascii NOT NULL,revision BIGINT UNSIGNED NOT NULL,before_data MEDIUMBLOB NOT NULL,after_data MEDIUMBLOB NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(uid,client_hash,revision)) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS stage_access_audit(revision BIGINT UNSIGNED PRIMARY KEY,before_data MEDIUMBLOB NOT NULL,after_data MEDIUMBLOB NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS battle_settlements(serial INT UNSIGNED PRIMARY KEY,reports MEDIUMBLOB NOT NULL,result MEDIUMBLOB NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,

	`CREATE TABLE IF NOT EXISTS friends(uid BIGINT UNSIGNED NOT NULL,friend_uid BIGINT UNSIGNED NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(uid,friend_uid),FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE,FOREIGN KEY(friend_uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS character_creations(uid BIGINT UNSIGNED PRIMARY KEY,nickname VARCHAR(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL UNIQUE,request VARBINARY(68) NOT NULL,FOREIGN KEY(uid) REFERENCES accounts(uid)) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS talisman_uses(uid BIGINT UNSIGNED NOT NULL,operation_id VARCHAR(128) CHARACTER SET ascii NOT NULL,request BINARY(16) NOT NULL,PRIMARY KEY(uid,operation_id),FOREIGN KEY(uid) REFERENCES accounts(uid)) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS talisman_repairs(uid BIGINT UNSIGNED NOT NULL,operation_id VARCHAR(128) CHARACTER SET ascii NOT NULL,instance INT UNSIGNED NOT NULL,material INT UNSIGNED NOT NULL,quantity INT UNSIGNED NOT NULL,capacity INT UNSIGNED NOT NULL,PRIMARY KEY(uid,operation_id),FOREIGN KEY(uid) REFERENCES accounts(uid)) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS mailbox(id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,uid BIGINT UNSIGNED NOT NULL,list_record VARBINARY(339) NOT NULL,detail_record VARBINARY(136) NOT NULL,is_read BOOLEAN NOT NULL DEFAULT FALSE,deleted BOOLEAN NOT NULL DEFAULT FALSE,claimed BOOLEAN NOT NULL DEFAULT FALSE,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP,INDEX inbox(uid,deleted,id),FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS mail_attachments(id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,mail_id INT UNSIGNED NOT NULL UNIQUE,uid BIGINT UNSIGNED NOT NULL,grant_record VARBINARY(68) NOT NULL,expiry_days INT UNSIGNED NOT NULL DEFAULT 0,claimed_instance INT UNSIGNED NULL,FOREIGN KEY(mail_id) REFERENCES mailbox(id) ON DELETE CASCADE,FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS gift_receipts(uid BIGINT UNSIGNED NOT NULL,operation_id VARCHAR(128) CHARACTER SET ascii NOT NULL,request_hash BINARY(32) NOT NULL,recipient BIGINT UNSIGNED NOT NULL,mail_id INT UNSIGNED NOT NULL,cost INT UNSIGNED NOT NULL,PRIMARY KEY(uid,operation_id),FOREIGN KEY(uid) REFERENCES accounts(uid)) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS honour_stats(period INT UNSIGNED NOT NULL,uid BIGINT UNSIGNED NOT NULL,points BIGINT UNSIGNED NOT NULL,games BIGINT UNSIGNED NOT NULL,wins BIGINT UNSIGNED NOT NULL,PRIMARY KEY(period,uid),INDEX honour_rank(period,points),FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS inventory(
        uid BIGINT UNSIGNED NOT NULL ,
        instance INT UNSIGNED NOT NULL ,
        record VARBINARY(68) NOT NULL ,
        PRIMARY KEY(uid ,
        instance) ,
        FOREIGN KEY(uid) REFERENCES accounts(uid)
    ) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS inventory_expirations(uid BIGINT UNSIGNED NOT NULL,instance INT UNSIGNED NOT NULL,expires_at BIGINT NOT NULL,processed BOOLEAN NOT NULL DEFAULT FALSE,PRIMARY KEY(uid,instance),INDEX due_inventory(uid,processed,expires_at),FOREIGN KEY(uid,instance) REFERENCES inventory(uid,instance) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS weapon_upgrades(uid BIGINT UNSIGNED NOT NULL,operation_id VARCHAR(128) CHARACTER SET ascii NOT NULL,instance INT UNSIGNED NOT NULL,success BOOLEAN NOT NULL,cost INT UNSIGNED NOT NULL,score_cost INT UNSIGNED NOT NULL,odds INT UNSIGNED NOT NULL,roll INT UNSIGNED NOT NULL,before_record VARBINARY(68) NOT NULL,after_record VARBINARY(68) NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(uid,operation_id),FOREIGN KEY(uid) REFERENCES accounts(uid)) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS offers(
        catalog_key INT UNSIGNED PRIMARY KEY ,
        category TINYINT UNSIGNED NOT NULL ,
        variant TINYINT UNSIGNED NOT NULL ,
        record VARBINARY(108) NOT NULL ,
        grant_record VARBINARY(68) NOT NULL ,
        enabled BOOLEAN NOT NULL DEFAULT TRUE
    ) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS offer_lifetimes(catalog_key INT UNSIGNED PRIMARY KEY,days INT UNSIGNED NOT NULL,FOREIGN KEY(catalog_key) REFERENCES offers(catalog_key) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS item_definitions(definition_key INT UNSIGNED PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,record VARBINARY(68) NOT NULL,days INT UNSIGNED NOT NULL) ENGINE=InnoDB`,
	seedDefinitionsSQL,
	`CREATE TABLE IF NOT EXISTS tutorial_rewards(uid BIGINT UNSIGNED PRIMARY KEY,reward MEDIUMBLOB NOT NULL,FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS renewal_receipts(uid BIGINT UNSIGNED NOT NULL,operation_id VARCHAR(128) NOT NULL,request_hash BINARY(32) NOT NULL,instance INT UNSIGNED NOT NULL,PRIMARY KEY(uid,operation_id),FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS renewal_reminders(uid BIGINT UNSIGNED NOT NULL,instance INT UNSIGNED NOT NULL,ignored_deadline BIGINT NOT NULL,PRIMARY KEY(uid,instance),FOREIGN KEY(uid,instance) REFERENCES inventory(uid,instance) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS level_reward_receipts(uid BIGINT UNSIGNED NOT NULL,level SMALLINT UNSIGNED NOT NULL,items MEDIUMBLOB NOT NULL,PRIMARY KEY(uid,level),FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS purchases(
        uid BIGINT UNSIGNED NOT NULL ,
        operation_id VARCHAR(128) CHARACTER SET ascii NOT NULL ,
        request_hash BINARY(32) NOT NULL ,
        balance BIGINT UNSIGNED NOT NULL ,
        item_record VARBINARY(68) NOT NULL ,
        catalog_record VARBINARY(108) NOT NULL ,
        created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ,
        PRIMARY KEY(uid ,
        operation_id) ,
        FOREIGN KEY(uid) REFERENCES accounts(uid)
    ) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS wallet_operations(
        operation_id VARCHAR(128) CHARACTER SET ascii PRIMARY KEY ,
        uid BIGINT UNSIGNED NOT NULL ,
        mode VARCHAR(4) NOT NULL ,
        amount BIGINT UNSIGNED NOT NULL ,
        before_balance BIGINT UNSIGNED NOT NULL ,
        after_balance BIGINT UNSIGNED NOT NULL ,
        created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ,
        FOREIGN KEY(uid) REFERENCES accounts(uid)
    ) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS counters(
        name VARCHAR(32) PRIMARY KEY ,
        value BIGINT UNSIGNED NOT NULL
    ) ENGINE=InnoDB`,
	`INSERT IGNORE INTO counters(name,value) VALUES('battle',0)`,
	`CREATE TABLE IF NOT EXISTS training_claims(uid BIGINT UNSIGNED NOT NULL,operation_id VARCHAR(128) NOT NULL,started BIGINT NOT NULL,training_rank INT UNSIGNED NOT NULL,revision BIGINT UNSIGNED NOT NULL,experience INT UNSIGNED NOT NULL,PRIMARY KEY(uid,operation_id),UNIQUE KEY training_cycle(uid,started)) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS training_ranks(uid BIGINT UNSIGNED PRIMARY KEY,training_rank INT UNSIGNED NOT NULL DEFAULT 0,FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS offer_recommendations(catalog_key BIGINT UNSIGNED PRIMARY KEY,enabled BOOLEAN NOT NULL) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS offer_recommendation_order(catalog_key BIGINT UNSIGNED PRIMARY KEY,priority INT NOT NULL DEFAULT 0) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS login_error_rules(id TINYINT UNSIGNED PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,rules MEDIUMBLOB NOT NULL) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS login_error_rules_audit(revision BIGINT UNSIGNED PRIMARY KEY,before_data MEDIUMBLOB NOT NULL,after_data MEDIUMBLOB NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS treasure_rules(id TINYINT UNSIGNED PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,rules MEDIUMBLOB NOT NULL) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS treasure_rules_audit(revision BIGINT UNSIGNED PRIMARY KEY,before_data MEDIUMBLOB NOT NULL,after_data MEDIUMBLOB NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS vip_shop_rules(id TINYINT PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,rules MEDIUMBLOB NOT NULL) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS vip_shop_rules_audit(revision BIGINT UNSIGNED PRIMARY KEY,before_data MEDIUMBLOB NOT NULL,after_data MEDIUMBLOB NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS task_progress(uid BIGINT UNSIGNED NOT NULL,task_key SMALLINT UNSIGNED NOT NULL,state TINYINT UNSIGNED NOT NULL,baseline BINARY(116) NOT NULL,rule_revision BIGINT UNSIGNED NOT NULL,rule_data MEDIUMBLOB NULL,PRIMARY KEY(uid,task_key),FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS extended_task_progress(uid BIGINT UNSIGNED NOT NULL,task_key SMALLINT UNSIGNED NOT NULL,cycle VARCHAR(10) NOT NULL,state TINYINT UNSIGNED NOT NULL,rule_revision BIGINT UNSIGNED NOT NULL,rule_data MEDIUMBLOB NOT NULL,counts BINARY(12) NOT NULL,PRIMARY KEY(uid,task_key,cycle),FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS task_rewards(uid BIGINT UNSIGNED NOT NULL,task_key SMALLINT UNSIGNED NOT NULL,rule_revision BIGINT UNSIGNED NOT NULL,experience INT UNSIGNED NOT NULL,gold INT UNSIGNED NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(uid,task_key),FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS title_rewards(uid BIGINT UNSIGNED NOT NULL,title_level TINYINT UNSIGNED NOT NULL,choices BLOB NOT NULL,claimed_key INT UNSIGNED NULL,claimed_instance INT UNSIGNED NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(uid,title_level),FOREIGN KEY(uid) REFERENCES accounts(uid) ON DELETE CASCADE) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS training(
        uid BIGINT UNSIGNED PRIMARY KEY ,
        started BIGINT NULL ,
        FOREIGN KEY(uid) REFERENCES accounts(uid)
    ) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS horn_settings(id TINYINT PRIMARY KEY,revision BIGINT UNSIGNED NOT NULL,channel_enabled BOOLEAN NOT NULL,realm_enabled BOOLEAN NOT NULL,mood_enabled BOOLEAN NOT NULL) ENGINE=InnoDB`,
	`INSERT IGNORE INTO horn_settings VALUES(1,1,TRUE,TRUE,TRUE)`,
	`CREATE TABLE IF NOT EXISTS horn_events(id CHAR(32) PRIMARY KEY,uid BIGINT UNSIGNED NOT NULL,kind INT UNSIGNED NOT NULL,cost SMALLINT UNSIGNED NOT NULL,message TEXT NOT NULL,queued INT UNSIGNED NOT NULL DEFAULT 0,skipped INT UNSIGNED NOT NULL DEFAULT 0,status VARCHAR(16) NOT NULL DEFAULT 'pending',created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX(uid,created)) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS consumption_events (
	    uid BIGINT UNSIGNED NOT NULL,
	    battle INT UNSIGNED NOT NULL,
	    sequence INT UNSIGNED NOT NULL,
	    instance INT UNSIGNED NOT NULL,
	    signature VARBINARY(40) NOT NULL,
	    PRIMARY KEY(uid,battle,sequence),
	    FOREIGN KEY(uid) REFERENCES accounts(uid)
	) ENGINE=InnoDB`,
}

func Open(dsn string) (*Store, error) { return open(dsn, true) }

// A running local server already migrated its database. GM requests must not
// repeat every DDL statement across the SSH database tunnel.
func OpenExisting(dsn string) (*Store, error) { return open(dsn, false) }

func open(dsn string, initialize bool) (*Store, error) {
	dsn, err := boundedDSN(dsn)
	if err != nil {
		return nil, err
	}
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(8)
	database.SetMaxIdleConns(4)
	database.SetConnMaxLifetime(3 * time.Minute)
	store := &Store{DB: database, wordFilter: &wordCache{}}
	contextWithTimeout, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = database.PingContext(contextWithTimeout); err == nil && initialize {
		for _, statement := range schema {
			if _, err = database.ExecContext(contextWithTimeout, statement); err != nil {
				break
			}
		}
	}
	if err != nil {
		database.Close()
		return nil, err
	}
	return store, nil
}
func (account Account) InventoryBytes() []byte { return bytes.Join(account.Inventory, nil) }
func (store *Store) Authenticate(account, legacy string) (Account, error) {
	if !accountPattern.MatchString(account) || !legacyPattern.MatchString(legacy) {
		return Account{}, ErrDenied
	}
	account = strings.ToLower(account)
	var uid uint64
	var salt, digest []byte
	lookupErr := store.DB.QueryRow(`SELECT uid,legacy_salt,legacy_digest FROM accounts WHERE account=?`, account).Scan(&uid, &salt, &digest)
	// Do comparable expensive work for an unknown account; never expose account existence.
	if lookupErr != nil {
		salt = make([]byte, 16)
		digest = make([]byte, 32)
	}
	actual, err := scrypt.Key([]byte(strings.ToLower(legacy)), salt, 32768, 8, 3, 32)
	if err != nil || lookupErr != nil || len(salt) != 16 || len(digest) != 32 || subtle.ConstantTimeCompare(actual, digest) != 1 {
		return Account{}, ErrDenied
	}
	ban, err := store.AccountBan(uid)
	if err != nil {
		return Account{}, err
	}
	if ban.Active(time.Now().Unix()) {
		return Account{}, ErrAccountBanned
	}
	result, err := store.RoleManager().Snapshot(uid)
	result.BanGeneration = ban.Generation
	return result, err
}
func NewAccount(uid uint64, name, password string) (Account, error) {
	if uid == 0 || !accountPattern.MatchString(name) || len(password) < 6 || len(password) > 128 {
		return Account{}, ErrDenied
	}
	account := Account{UID: uid, Account: strings.ToLower(name), Profile: make([]byte, 360), Salt: make([]byte, 16), LegacySalt: make([]byte, 16)}
	if _, err := rand.Read(account.Salt); err != nil {
		return account, err
	}
	if _, err := rand.Read(account.LegacySalt); err != nil {
		return account, err
	}
	var err error
	account.Digest, err = scrypt.Key([]byte(password), account.Salt, 32768, 8, 3, 32)
	if err != nil {
		return account, err
	}
	passwordHash := sha256.Sum256(append([]byte("xfmRn9z7K1wTfvBYhpCwZmE8yLWN1oLv"), []byte(password)...))
	account.LegacyDigest, err = scrypt.Key([]byte(hex.EncodeToString(passwordHash[:])), account.LegacySalt, 32768, 8, 3, 32)
	if err != nil {
		return account, err
	}
	// An authenticated account is not a character. The all-zero profile causes
	// profileReady to send 1125; only the client's 1150 creates the character.
	return account, nil
}

// NewAccountWithStarterCharacter explicitly constructs the historical test
// fixture. Registration must use NewAccount and the native creation flow.
func NewAccountWithStarterCharacter(uid uint64, name, password string) (Account, error) {
	account, err := NewAccount(uid, name, password)
	if err != nil {
		return account, err
	}
	account.Nickname = name
	protocol.WriteUint32(account.Profile, 0, 1)
	copy(account.Profile[4:25], name)
	account.Profile[122] = 1
	account.Profile[124] = 1
	for index, equipment := range [][3]uint32{{121005, 12, 4}, {131011, 13, 3}, {141005, 14, 7}, {151005, 15, 2}, {161005, 16, 6}, {171005, 17, 5}, {253030, 25, 8}} {
		record := make([]byte, 68)
		protocol.WriteUint32(record, 0, 0x100000+uint32(index))
		record[4] = byte(equipment[1])
		protocol.WriteUint32(record, 5, equipment[0])
		protocol.WriteUint32(record, 13, 8760)
		protocol.WriteUint16(record, 17, uint16(equipment[2]))
		account.Inventory = append(account.Inventory, record)
	}
	return account, nil
}
func insertAccount(transaction *sql.Tx, account Account) error {
	if len(account.Profile) != 360 || len(account.Salt) != 16 || len(account.Digest) != 32 || len(account.LegacySalt) != 16 || len(account.LegacyDigest) != 32 || account.UID == 0 || !accountPattern.MatchString(account.Account) {
		return ErrDenied
	}
	_, err := transaction.Exec(`INSERT INTO accounts VALUES(?,?,?,?,?,?,?,?,?,?)`, account.UID, strings.ToLower(account.Account), account.Nickname, account.Profile, account.Salt, account.Digest, account.LegacySalt, account.LegacyDigest, account.Gold, account.Tickets)
	if err != nil {
		return err
	}
	for _, record := range account.Inventory {
		if len(record) != 68 {
			return ErrDenied
		}
		if _, err = transaction.Exec(`INSERT INTO inventory VALUES(?,?,?)`, account.UID, protocol.ReadUint32(record, 0), record); err != nil {
			return err
		}
	}
	return nil
}
func (store *Store) Create(account Account) error {
	transaction, transactionCancel, err := beginTransaction(store.DB)
	defer transactionCancel()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if err = insertAccount(transaction, account); err != nil {
		return err
	}
	return transaction.Commit()
}

// ResetPassword changes only credentials, preserving character and inventory.
func (store *Store) ResetPassword(uid uint64, name, password string) error {
	credentials, err := NewAccount(uid, name, password)
	if err != nil {
		return err
	}
	result, err := store.DB.Exec(`UPDATE accounts SET salt=?,digest=?,legacy_salt=?,legacy_digest=? WHERE uid=? AND account=?`, credentials.Salt, credentials.Digest, credentials.LegacySalt, credentials.LegacyDigest, uid, credentials.Account)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrDenied
	}
	return nil
}
func (store *Store) Import(export Export) error {
	transaction, transactionCancel, err := beginTransaction(store.DB)
	defer transactionCancel()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var count int
	if err = transaction.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("import requires empty game database")
	}
	for _, account := range export.Accounts {
		if err = insertAccount(transaction, account); err != nil {
			return err
		}
	}
	for _, offer := range export.Offers {
		if len(offer.Record) != 108 || len(offer.Grant) != 68 {
			return ErrDenied
		}
		if _, err = transaction.Exec(`INSERT INTO offers VALUES(?,?,?,?,?,TRUE)`, offer.Key, offer.Category, offer.Variant, offer.Record, offer.Grant); err != nil {
			return err
		}
	}
	if _, err := transaction.Exec(seedDefinitionsSQL); err != nil {
		return err
	}
	return transaction.Commit()
}

func GBK(text string) []byte {
	encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text))
	if err != nil {
		return []byte("?")
	}
	return encoded
}
