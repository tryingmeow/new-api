package model

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The upgrade fixture uses unchanged rc-baseline user/channel schemas, without
// the newly introduced scoped-token table or legacy retirement option.
func TestRC45MergeDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			dsn := "local"
			if dialect == "mysql" {
				dsn = os.Getenv("TEST_MYSQL_DSN")
			}
			if dialect == "postgres" {
				dsn = os.Getenv("TEST_POSTGRES_DSN")
			}
			if dsn == "" {
				t.Skip("test database DSN is not configured")
			}
			oldPath := common.SQLitePath
			common.SQLitePath = filepath.Join(t.TempDir(), "merge.db")
			defer func() { common.SQLitePath = oldPath }()
			t.Setenv("RC45_MERGE_TEST_DSN", dsn)
			db, dbType, err := chooseDB("RC45_MERGE_TEST_DSN", false)
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			defer sqlDB.Close()
			sqlDB.SetMaxOpenConns(1)
			oldDB, oldLogDB := DB, LOG_DB
			oldType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
			oldDeadline, oldMemory := legacyAccessTokenRetireAt.Load(), common.MemoryCacheEnabled
			DB, LOG_DB = db, db
			common.SetDatabaseTypes(dbType, dbType)
			common.MemoryCacheEnabled = false
			initCol()
			defer func() {
				DB, LOG_DB = oldDB, oldLogDB
				common.SetDatabaseTypes(oldType, oldLogType)
				legacyAccessTokenRetireAt.Store(oldDeadline)
				common.MemoryCacheEnabled = oldMemory
				initCol()
			}()
			// Fresh startup, then restart: schema and one-time deadline must survive.
			require.NoError(t, migrateDB())
			freshDeadline := LegacyAccessTokenRetireAt()
			require.NoError(t, migrateDB())
			assert.Equal(t, freshDeadline, LegacyAccessTokenRetireAt())
			require.True(t, db.Migrator().HasTable(&UserAccessToken{}))
			// Remove only rc45's new schema/state to represent the previous release.
			require.NoError(t, db.Migrator().DropTable(&UserAccessToken{}))
			require.NoError(t, db.Where(commonKeyCol+" = ?", legacyAccessTokenRetireAtKey).Delete(&Option{}).Error)
			require.NoError(t, db.Unscoped().Where("username = ?", "rc45-upgrade").Delete(&User{}).Error)
			require.NoError(t, db.Where("channel_id >= ? AND channel_id < ?", 940000, 940004).Delete(&Ability{}).Error)
			require.NoError(t, db.Where("id >= ? AND id < ?", 940000, 940004).Delete(&Channel{}).Error)
			legacy := "0123456789abcdef0123456789abcdef"
			user := User{Username: "rc45-upgrade", Password: "synthetic-hash", Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, Quota: 12345, AccessToken: &legacy}
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, migrateDB())
			deadline := LegacyAccessTokenRetireAt()
			require.NoError(t, migrateDB())
			assert.Equal(t, deadline, LegacyAccessTokenRetireAt())
			var preserved User
			require.NoError(t, db.First(&preserved, user.Id).Error)
			assert.Equal(t, 12345, preserved.Quota)
			require.NotNil(t, preserved.AccessToken)
			assert.Equal(t, legacy, *preserved.AccessToken)
			token := UserAccessToken{UserId: user.Id, Name: "fixture", TokenHash: AccessTokenFingerprint("synthetic-pat"), TokenHint: "hint", Scopes: `["profile:read"]`}
			require.NoError(t, db.Create(&token).Error)
			duplicate := token
			duplicate.Id = 0
			assert.Error(t, db.Create(&duplicate).Error, "token fingerprint must remain unique")
			require.NoError(t, migrateDB())
			var saved UserAccessToken
			require.NoError(t, db.First(&saved, token.Id).Error)
			assert.Equal(t, token.TokenHash, saved.TokenHash)
			// Filtering removes the exact-name candidate; normalized fallback must count
			// both eligible priorities and exclude websocket-ineligible channels.
			modelName := "deepseek-flash@effort:max"
			for i, item := range []struct {
				model    string
				ws       bool
				priority int64
			}{{modelName, false, 100}, {"deepseek-flash", true, 100}, {"deepseek-flash", true, 10}, {"deepseek-flash", false, 1}} {
				priority := item.priority
				ch := Channel{Id: 940000 + i, Name: "merge-routing", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Group: "rc45-routing", Models: item.model, Priority: &priority}
				if item.ws {
					setting := `{"responses_websocket_enabled":true}`
					ch.Setting = &setting
				}
				require.NoError(t, ch.Insert())
			}
			filters := []dto.ChannelFilter{{Kind: dto.FilterResponsesWebSocket}}
			for _, memory := range []bool{false, true} {
				common.MemoryCacheEnabled = memory
				InitChannelCache()
				for retry, wantID := range []int{940001, 940002, 940002} {
					ch, count, err := GetRandomSatisfiedChannel("rc45-routing", modelName, retry, filters)
					require.NoError(t, err)
					require.NotNil(t, ch)
					assert.Equal(t, 2, count)
					assert.Equal(t, wantID, ch.Id)
				}
			}
		})
	}
}
