// Package mariadb keeps saved requests in a MariaDB or MySQL database, so they are still
// there after Traffic Light restarts.
//
// It is optional and only ever holds what you saved: the log itself stays in memory. There is
// one table, trafficlight_saved, which is created if it is missing.
package mariadb

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/pokemaplx/TrafficLight/internal/pogo"
	"github.com/pokemaplx/TrafficLight/internal/web"
)

const (
	// a database that isn't up yet is waited for this long, which is what a container
	// started next to its database needs
	startupWait = 30 * time.Second
	// nothing here is worth waiting longer for
	queryTimeout = 15 * time.Second
)

const schema = `CREATE TABLE IF NOT EXISTS trafficlight_saved (
	id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
	time_ms BIGINT NOT NULL,
	rpc_id BIGINT NOT NULL,
	rpc_status BIGINT NOT NULL,
	rpc_handle BIGINT NULL,
	protos LONGBLOB NOT NULL,
	KEY time_ms (time_ms)
)`

type Archive struct {
	db *sql.DB
}

var _ web.Archive = (*Archive)(nil)

// Open connects to the database a url names and makes sure its table is there.
//
// The url is mariadb://user:password@host:3306/database (mysql:// works as well, and the port
// can be left out), or a DSN the way the Go driver writes them, user:password@tcp(host)/database.
func Open(databaseURL string) (*Archive, error) {
	config, err := parse(databaseURL)
	if err != nil {
		return nil, err
	}
	connector, err := mysql.NewConnector(config)
	if err != nil {
		return nil, err
	}

	db := sql.OpenDB(connector)
	// saving is something a person does by hand, a couple of connections are plenty
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)

	deadline := time.Now().Add(startupWait)
	waiting := false
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = db.PingContext(ctx)
		cancel()
		if err == nil || time.Now().After(deadline) || !temporary(err) {
			break
		}
		if !waiting {
			waiting = true
			fmt.Printf("The database isn't reachable yet, waiting up to %d seconds for it\n", int(startupWait.Seconds()))
		}
		time.Sleep(time.Second)
	}
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		_, err = db.ExecContext(ctx, schema)
		cancel()
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Archive{db: db}, nil
}

// temporary tells a database that isn't reachable yet from one that refuses us for good,
// like a wrong password
func temporary(err error) bool {
	var refused *mysql.MySQLError
	return !errors.As(err, &refused)
}

// parse never puts the url into an error: it holds the password
func parse(databaseURL string) (*mysql.Config, error) {
	dsn := databaseURL
	if scheme, _, found := strings.Cut(databaseURL, "://"); found {
		if scheme != "mariadb" && scheme != "mysql" {
			return nil, fmt.Errorf("the database url has to start with mariadb:// or mysql://, not %s://", scheme)
		}
		parsed, err := url.Parse(databaseURL)
		if err != nil || parsed.Hostname() == "" {
			return nil, errors.New("the database url isn't a valid url. Special characters in the password have to be percent-encoded")
		}

		base := mysql.NewConfig()
		base.User = parsed.User.Username()
		base.Passwd, _ = parsed.User.Password()
		base.Net = "tcp"
		base.Addr = parsed.Host
		if parsed.Port() == "" {
			base.Addr = net.JoinHostPort(parsed.Hostname(), "3306")
		}
		base.DBName = strings.Trim(parsed.Path, "/")
		dsn = base.FormatDSN()
		if parsed.RawQuery != "" {
			separator := "?"
			if strings.Contains(dsn, "?") {
				separator = "&"
			}
			dsn += separator + parsed.RawQuery
		}
	}

	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, errors.New("the database url isn't valid. It looks like mariadb://user:password@host:3306/database")
	}
	if config.DBName == "" {
		return nil, errors.New("the database url doesn't name a database")
	}
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Second
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = queryTimeout
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = queryTimeout
	}
	// the driver prints what goes wrong on its own, on top of the errors it returns. Those
	// are reported already, and while a database starts up it would print once a second
	config.Logger = &mysql.NopLogger{}
	return config, nil
}

func (a *Archive) Close() error {
	return a.db.Close()
}

// Load returns everything that is saved, oldest first
func (a *Archive) Load() ([]web.Archived, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rows, err := a.db.QueryContext(ctx, `SELECT id, time_ms, rpc_id, rpc_status, rpc_handle, protos FROM trafficlight_saved ORDER BY time_ms, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var saved []web.Archived
	for rows.Next() {
		var entry web.Archived
		var handle sql.NullInt64
		var protos []byte
		if err := rows.Scan(&entry.Key, &entry.Time, &entry.Request.RPCID, &entry.Request.RPCStatus, &handle, &protos); err != nil {
			return nil, err
		}
		if handle.Valid {
			entry.Request.RPCHandle = &handle.Int64
		}
		if entry.Request.Protos, err = decodeProtos(protos); err != nil {
			return nil, fmt.Errorf("saved request %d: %w", entry.Key, err)
		}
		saved = append(saved, entry)
	}
	return saved, rows.Err()
}

// Save stores a request and returns the key it can be deleted by
func (a *Archive) Save(saved web.Archived) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	var handle sql.NullInt64
	if saved.Request.RPCHandle != nil {
		handle = sql.NullInt64{Int64: *saved.Request.RPCHandle, Valid: true}
	}
	result, err := a.db.ExecContext(ctx,
		`INSERT INTO trafficlight_saved (time_ms, rpc_id, rpc_status, rpc_handle, protos) VALUES (?, ?, ?, ?, ?)`,
		saved.Time, saved.Request.RPCID, saved.Request.RPCStatus, handle, encodeProtos(saved.Request.Protos))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (a *Archive) Delete(keys []int64) error {
	if len(keys) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	arguments := make([]any, len(keys))
	for i, key := range keys {
		arguments[i] = key
	}
	_, err := a.db.ExecContext(ctx,
		`DELETE FROM trafficlight_saved WHERE id IN (?`+strings.Repeat(",?", len(keys)-1)+`)`, arguments...)
	return err
}

// The protos of a request are stored the way they arrived, still encoded, as one blob:
// a version byte, then for every proto its method and its request and response, each with
// their length in front. Numbers are varints.
const protosVersion = 1

func encodeProtos(protos []pogo.RawProto) []byte {
	size := 1
	for i := range protos {
		size += 3*binary.MaxVarintLen64 + len(protos[i].Request) + len(protos[i].Response)
	}
	blob := make([]byte, 1, size)
	blob[0] = protosVersion
	for i := range protos {
		blob = binary.AppendVarint(blob, protos[i].Method)
		blob = binary.AppendUvarint(blob, uint64(len(protos[i].Request)))
		blob = append(blob, protos[i].Request...)
		blob = binary.AppendUvarint(blob, uint64(len(protos[i].Response)))
		blob = append(blob, protos[i].Response...)
	}
	return blob
}

var errCorrupt = errors.New("its protos are not something Traffic Light wrote")

// decodeProtos reads a blob back. The protos point into it instead of copying it
func decodeProtos(blob []byte) ([]pogo.RawProto, error) {
	if len(blob) == 0 || blob[0] != protosVersion {
		return nil, errCorrupt
	}
	blob = blob[1:]

	bytes := func() ([]byte, bool) {
		length, size := binary.Uvarint(blob)
		if size <= 0 || length > uint64(len(blob)-size) {
			return nil, false
		}
		end := size + int(length)
		// capped, so appending to one proto can't run into the next
		data := blob[size:end:end]
		blob = blob[end:]
		return data, true
	}

	var protos []pogo.RawProto
	for len(blob) > 0 {
		method, size := binary.Varint(blob)
		if size <= 0 {
			return nil, errCorrupt
		}
		blob = blob[size:]

		request, ok := bytes()
		if !ok {
			return nil, errCorrupt
		}
		response, ok := bytes()
		if !ok {
			return nil, errCorrupt
		}
		protos = append(protos, pogo.RawProto{Method: method, Request: request, Response: response})
	}
	return protos, nil
}
