package app

import (
	"fmt"
	"log/slog"
	"net/http"

	jwkservice "github.com/disillusioned-labs/audit/internal/service/jwks"
	organizationservice "github.com/disillusioned-labs/audit/internal/service/organization"
	organizationinvitationservice "github.com/disillusioned-labs/audit/internal/service/organization_invitation"
	organizationmemberservice "github.com/disillusioned-labs/audit/internal/service/organization_member"
	"github.com/disillusioned-labs/authkit"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/disillusioned-labs/audit/internal/config"
	"github.com/disillusioned-labs/audit/internal/platform/cache"
	"github.com/disillusioned-labs/audit/internal/repository"
	"github.com/disillusioned-labs/audit/internal/server"
	authservice "github.com/disillusioned-labs/audit/internal/service/auth"
)

func buildDeps(pool *pgxpool.Pool, rdb *goredis.Client, redisRequired bool, cache cache.Cache, authCfg config.AuthConfig, log *slog.Logger) (server.Deps, error) {
	repo := repository.NewStore(pool)

	return server.Deps{
		AuthService:                   auth,
		JwksService:                   jwksService,
		OrganizationService:           organizationService,
		OrganizationMemberService:     organizationMemberService,
		OrganizationInvitationService: organizationInvitationService,
		Verifier:                      verifier,
		Pool:                          pool,
		Redis:                         rdb,
		RedisRequired:                 redisRequired,
		Cache:                         cache,
	}, nil
}
