package model

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func modelRoutingTestPolicy() ModelRoutingPolicy {
	return ModelRoutingPolicy{Enabled: true, Rules: []ModelRoutingRule{
		{Name: "图片和工具", Image: "present", Tools: "present", Candidates: []ModelRoutingCandidate{
			{Model: "multimodal", Endpoints: []string{"chat", "responses"}, Vision: true, Tools: true},
		}},
		{Name: "Text without tools", Image: "absent", Tools: "absent", Candidates: []ModelRoutingCandidate{
			{Model: "text", Endpoints: []string{"chat"}},
		}},
		{Name: "Default", Image: "any", Tools: "any", Candidates: []ModelRoutingCandidate{
			{Model: "fallback", Endpoints: []string{"responses"}, Tools: true},
		}},
	}}
}

func TestModelRoutingValidation(t *testing.T) {
	require.NoError(t, validateModelRouting(&ModelRoutingPolicy{}))
	valid := modelRoutingTestPolicy()
	valid.Rules[0].Name = strings.Repeat("图", 128)
	valid.Rules[0].Candidates[0].Model = strings.Repeat("图", 85)
	require.NoError(t, validateModelRouting(&valid))

	for _, tc := range []struct {
		name   string
		change func(*ModelRoutingPolicy)
	}{
		{"enabled without rules", func(p *ModelRoutingPolicy) { p.Rules = nil }},
		{"missing default", func(p *ModelRoutingPolicy) { p.Rules = p.Rules[:2] }},
		{"multiple defaults", func(p *ModelRoutingPolicy) { p.Rules[0].Image, p.Rules[0].Tools = "any", "any" }},
		{"default not last", func(p *ModelRoutingPolicy) { p.Rules[0], p.Rules[2] = p.Rules[2], p.Rules[0] }},
		{"invalid condition", func(p *ModelRoutingPolicy) { p.Rules[0].Image = "maybe" }},
		{"empty name", func(p *ModelRoutingPolicy) { p.Rules[0].Name = "  " }},
		{"name too long", func(p *ModelRoutingPolicy) { p.Rules[0].Name = strings.Repeat("图", 129) }},
		{"too many rules", func(p *ModelRoutingPolicy) { p.Rules = make([]ModelRoutingRule, 33) }},
		{"no candidates", func(p *ModelRoutingPolicy) { p.Rules[0].Candidates = nil }},
		{"too many candidates", func(p *ModelRoutingPolicy) { p.Rules[0].Candidates = make([]ModelRoutingCandidate, 9) }},
		{"recursive alias", func(p *ModelRoutingPolicy) { p.Rules[0].Candidates[0].Model = "auto" }},
		{"model ID too long", func(p *ModelRoutingPolicy) { p.Rules[0].Candidates[0].Model = strings.Repeat("图", 86) }},
		{"repeated model", func(p *ModelRoutingPolicy) { r := &p.Rules[0]; r.Candidates = append(r.Candidates, r.Candidates[0]) }},
		{"missing endpoints", func(p *ModelRoutingPolicy) { p.Rules[0].Candidates[0].Endpoints = nil }},
		{"unknown endpoint", func(p *ModelRoutingPolicy) { p.Rules[0].Candidates[0].Endpoints = []string{"embedding"} }},
		{"repeated endpoint", func(p *ModelRoutingPolicy) { p.Rules[0].Candidates[0].Endpoints = []string{"chat", "chat"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := modelRoutingTestPolicy()
			tc.change(&policy)
			var message *common.Message
			assert.ErrorAs(t, validateModelRouting(&policy), &message)
		})
	}
}

func TestModelRoutingDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.ToSlash(filepath.Join(t.TempDir(), "routing.db")) + "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "model_routing_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			previousDB, previousType := DB, common.MainDatabaseType()
			previousRouting, previousRequest := modelRoutingSnapshot.Load(), requestPolicySnapshot.Load()
			common.OptionMapRWMutex.Lock()
			previousOptions := common.OptionMap
			common.OptionMap = make(map[string]string)
			common.OptionMapRWMutex.Unlock()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			modelRoutingSnapshot.Store(nil)
			t.Cleanup(func() {
				DB = previousDB
				common.SetMainDatabaseType(previousType)
				modelRoutingSnapshot.Store(previousRouting)
				requestPolicySnapshot.Store(previousRequest)
				common.OptionMapRWMutex.Lock()
				common.OptionMap = previousOptions
				common.OptionMapRWMutex.Unlock()
				assert.NoError(t, db.Migrator().DropTable(&Ability{}, &Option{}, &SystemTask{}))
				assert.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&Ability{}, &Option{}, &SystemTask{}))
			var version string
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database version: %s", version)

			initial, err := LoadModelRouting()
			require.NoError(t, err)
			assert.False(t, initial.Enabled)
			assert.Zero(t, initial.Revision)
			assert.NotNil(t, initial.Rules)
			assert.Error(t, UpdateOption("MODEL_ROUTING.POLICY", `{"enabled":true,"revision":10}`))

			// Both writers start without a row or a local revision. Exactly one
			// must commit; the other must get the same conflict as a later stale save.
			start := make(chan struct{})
			var workers sync.WaitGroup
			results := make([]error, 2)
			for i := range results {
				workers.Go(func() {
					<-start
					_, results[i] = SaveModelRouting(modelRoutingTestPolicy())
				})
			}
			close(start)
			workers.Wait()
			var successes int
			for _, err := range results {
				if err == nil {
					successes++
				} else {
					require.ErrorIs(t, err, ErrModelRoutingConflict)
				}
			}
			require.Equal(t, 1, successes)
			original := CurrentModelRouting()
			assert.EqualValues(t, 1, original.Revision)
			assert.True(t, original.Enabled)
			if dialect == "mysql" {
				// Guard even under a database collation that equates different
				// Unicode strings to the same option primary key.
				const alternate = "model_routíng.policy"
				var sameKey Option
				if err := db.Take(&sameKey, &Option{Key: alternate}).Error; err == nil {
					require.ErrorIs(t, UpdateOption(alternate, `{"enabled":false,"revision":5,"rules":[]}`), errModelRoutingManagedOption)
				} else {
					require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				}
			}

			stale := modelRoutingTestPolicy()
			_, err = SaveModelRouting(stale)
			require.ErrorIs(t, err, ErrModelRoutingConflict)
			stale.Revision = original.Revision
			stale.Rules[0].Candidates[0].Model = "auto"
			_, err = SaveModelRouting(stale)
			require.Error(t, err)
			assert.Error(t, UpdateOption(modelRoutingOptionKey, `{"enabled":false,"revision":2,"rules":[]}`))
			assert.Error(t, UpdateOptionsBulk(map[string]string{modelRoutingOptionKey: "{}", "Notice": "must not write"}))
			var row Option
			require.ErrorIs(t, db.Take(&row, &Option{Key: "Notice"}).Error, gorm.ErrRecordNotFound)
			assert.Same(t, original, CurrentModelRouting())

			// An uppercase spelling is a different explicit model, even on a
			// database whose collation equates it to "auto".
			ability := Ability{Group: "default", Model: "AUTO", ChannelId: 1, Enabled: true}
			require.NoError(t, db.Create(&ability).Error)
			collision, err := ModelRoutingAliasConflict()
			require.NoError(t, err)
			assert.False(t, collision)
			ability.Model, ability.ChannelId = "auto", 2
			require.NoError(t, db.Create(&ability).Error)
			update := modelRoutingTestPolicy()
			update.Revision = original.Revision
			_, err = SaveModelRouting(update)
			require.ErrorIs(t, err, ErrModelRoutingAliasConflict)
			require.NoError(t, db.Model(&ability).Where(&Ability{ChannelId: 2}).Update("enabled", false).Error)

			updated, err := SaveModelRouting(update)
			require.NoError(t, err)
			require.EqualValues(t, 2, updated.Revision)
			var tasks int64
			require.NoError(t, db.Model(&SystemTask{}).Where("type = ?", SyncTaskType(SyncTopicOptions)).Count(&tasks).Error)
			assert.EqualValues(t, 2, tasks, "only committed saves notify other nodes")
			if dialect == "mysql" {
				require.NoError(t, UpdateOption("ModelRoutingMatrixUnrelatedOption", "unchanged behavior"))
			}
			update.Rules[0].Candidates[0].Endpoints[0] = "responses"
			update.Rules[0].Candidates[0].Model = "mutated"
			update.Rules[0].Name = "mutated"
			assert.Equal(t, "图片和工具", CurrentModelRouting().Rules[0].Name)
			assert.Equal(t, "multimodal", CurrentModelRouting().Rules[0].Candidates[0].Model)
			assert.Equal(t, "chat", CurrentModelRouting().Rules[0].Candidates[0].Endpoints[0])
			assert.EqualValues(t, 1, original.Revision, "requests holding the old snapshot must not see later edits")

			modelRoutingSnapshot.Store(nil) // another node's first option load
			require.NoError(t, LoadOptionsFromDatabase())
			loaded := CurrentModelRouting()
			assert.Equal(t, updated, loaded)
			require.NoError(t, db.Model(&Option{Key: modelRoutingOptionKey}).Update("value", `{"enabled":false,"revision":1,"rules":[]}`).Error)
			require.NoError(t, LoadOptionsFromDatabase())
			assert.Same(t, loaded, CurrentModelRouting(), "an older reload cannot replace the published revision")
			require.NoError(t, db.Model(&Option{Key: modelRoutingOptionKey}).Update("value", `{"enabled":true,"revision":3,"rules":[]}`).Error)
			require.NoError(t, LoadOptionsFromDatabase())
			assert.Same(t, loaded, CurrentModelRouting(), "an invalid reload cannot replace the published revision")
		})
	}
}

func TestModelRoutingDatabaseErrorDoesNotPublish(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB, previousType := DB, common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		assert.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&Option{}))
	original := CurrentModelRouting()
	// The abilities table is deliberately absent. An inventory read failure
	// must not be mistaken for no real auto model, and neither half commits.
	_, err = SaveModelRouting(modelRoutingTestPolicy())
	var message *common.Message
	require.Error(t, err)
	assert.False(t, errors.As(err, &message))
	assert.Same(t, original, CurrentModelRouting())
	var rows int64
	require.NoError(t, db.Model(&Option{}).Count(&rows).Error)
	assert.Zero(t, rows)
}
