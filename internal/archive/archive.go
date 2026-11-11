// Package archive is where saved requests go to outlive Traffic Light, i.e. a database.
//
// The log itself stays in memory: an archive only mirrors what is saved. A request is written
// to it when you save it, removed when you unsave it or clear the saved ones, and all of them
// are put back into the log when Traffic Light starts. mariadb is the one there is.
package archive

import "github.com/pokemaplx/TrafficLight/internal/traffic"

// Store keeps saved requests
type Store interface {
	// Load returns everything that is saved, oldest first
	Load() ([]Saved, error)
	// Save stores a request and returns the key it can be deleted by
	Save(saved Saved) (key int64, err error)
	Delete(keys []int64) error
}

// Saved is a saved request the way a store holds it
type Saved struct {
	Key int64
	// when it arrived, in unix milliseconds
	Time    int64
	Request traffic.Request
}
