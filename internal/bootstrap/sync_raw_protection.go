package bootstrap

import (
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
)

func (s *syncEngineStore) rawExternalMatches(resource string, identity ingestion.RawIdentity, shop int64, plain, stored string) bool {
	if resource != "order" {
		return plain == stored
	}
	indexer, ok := s.rawSealer.(crypto.BlindIndexer)
	return ok && indexer.MatchBlindIndex(ingestion.RawIndexDomain(identity, shop, "external"), []byte(plain), stored)
}
