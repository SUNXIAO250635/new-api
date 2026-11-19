package model

import (
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ModelRoutingAlias           = "auto"
	modelRoutingOptionKey       = "model_routing.policy"
	initialModelRoutingDocument = `{"enabled":false,"revision":0,"rules":[]}`
	maxModelRoutingRevision     = 1<<53 - 1 // Revisions must round-trip through the web console's JSON number.
	maxModelRoutingRules        = 32
	maxModelRoutingCandidates   = 8
	maxModelRoutingRuleName     = 128
	maxModelRoutingModelBytes   = 255
)

var (
	ErrModelRoutingConflict      = common.NewMessage("The model routing settings have changed. Reload before saving again.")
	ErrModelRoutingAliasConflict = common.NewMessage("An enabled model named auto already exists. Disable or rename it before enabling model routing.")
	errModelRoutingManagedOption = common.NewMessage("Use the Model Routing page to update this setting")
	modelRoutingSnapshot         atomic.Pointer[ModelRoutingPolicy]
	disabledModelRouting         = ModelRoutingPolicy{Rules: []ModelRoutingRule{}}
)

type ModelRoutingPolicy struct {
	Enabled  bool               `json:"enabled"`
	Revision int64              `json:"revision"`
	Rules    []ModelRoutingRule `json:"rules"`
}

type ModelRoutingRule struct {
	Name       string                  `json:"name"`
	Image      string                  `json:"image"`
	Tools      string                  `json:"tools"`
	Candidates []ModelRoutingCandidate `json:"candidates"`
}

type ModelRoutingCandidate struct {
	Model     string   `json:"model"`
	Endpoints []string `json:"endpoints"`
	Vision    bool     `json:"vision"`
	Tools     bool     `json:"tools"`
}

// CurrentModelRouting returns the current immutable policy, initially disabled.
// Callers must not modify the policy or any of its slices.
func CurrentModelRouting() *ModelRoutingPolicy {
	if policy := modelRoutingSnapshot.Load(); policy != nil {
		return policy
	}
	return &disabledModelRouting
}

// publishModelRouting lets option reloads and local saves race without an older
// read or just-committed write undoing a newer revision already on this node.
func publishModelRouting(policy *ModelRoutingPolicy) {
	for {
		current := modelRoutingSnapshot.Load()
		if current != nil && current.Revision >= policy.Revision {
			return
		}
		if modelRoutingSnapshot.CompareAndSwap(current, policy) {
			return
		}
	}
}

func validateModelRouting(policy *ModelRoutingPolicy) error {
	if policy.Revision < 0 || policy.Revision > maxModelRoutingRevision {
		return common.NewMessage("Invalid model routing revision")
	}
	if len(policy.Rules) > maxModelRoutingRules {
		return common.NewMessage("Model routing supports at most {{max}} rules", map[string]any{"max": maxModelRoutingRules})
	}
	if len(policy.Rules) == 0 && policy.Enabled {
		return common.NewMessage("Model routing requires exactly one default rule at the end")
	}
	for i, rule := range policy.Rules {
		if strings.TrimSpace(rule.Name) == "" || !utf8.ValidString(rule.Name) || utf8.RuneCountInString(rule.Name) > maxModelRoutingRuleName {
			return common.NewMessage("Rule {{rule}} must have a name of 1 to {{max}} characters", map[string]any{"rule": i + 1, "max": maxModelRoutingRuleName})
		}
		if !validModelRoutingCondition(rule.Image) || !validModelRoutingCondition(rule.Tools) {
			return common.NewMessage("Rule {{rule}} has an invalid condition", map[string]any{"rule": i + 1})
		}
		if (rule.Image == "any" && rule.Tools == "any") != (i == len(policy.Rules)-1) {
			return common.NewMessage("Model routing requires exactly one default rule at the end")
		}
		if len(rule.Candidates) == 0 || len(rule.Candidates) > maxModelRoutingCandidates {
			return common.NewMessage("Rule {{rule}} must have 1 to {{max}} candidates", map[string]any{"rule": i + 1, "max": maxModelRoutingCandidates})
		}
		for j, candidate := range rule.Candidates {
			if len(candidate.Model) > maxModelRoutingModelBytes || strings.TrimSpace(candidate.Model) == "" || !utf8.ValidString(candidate.Model) || candidate.Model == ModelRoutingAlias {
				return common.NewMessage("Candidate {{candidate}} in rule {{rule}} must specify a model other than auto with at most {{max}} bytes", map[string]any{"rule": i + 1, "candidate": j + 1, "max": maxModelRoutingModelBytes})
			}
			if slices.ContainsFunc(rule.Candidates[:j], func(prior ModelRoutingCandidate) bool { return prior.Model == candidate.Model }) {
				return common.NewMessage("Rule {{rule}} repeats a candidate model", map[string]any{"rule": i + 1})
			}
			if !validModelRoutingEndpoints(candidate.Endpoints) {
				return common.NewMessage("Candidate {{candidate}} in rule {{rule}} must list Chat Completions, Responses, or both without duplicates", map[string]any{"rule": i + 1, "candidate": j + 1})
			}
		}
	}
	return nil
}

func validModelRoutingCondition(condition string) bool {
	return condition == "any" || condition == "present" || condition == "absent"
}

func validModelRoutingEndpoints(endpoints []string) bool {
	if len(endpoints) == 0 || len(endpoints) > 2 || (len(endpoints) == 2 && endpoints[0] == endpoints[1]) {
		return false
	}
	for _, endpoint := range endpoints {
		if endpoint != "chat" && endpoint != "responses" {
			return false
		}
	}
	return true
}

func parseModelRouting(value string) (*ModelRoutingPolicy, error) {
	var policy *ModelRoutingPolicy
	if err := common.UnmarshalJsonStr(value, &policy); err != nil || policy == nil || policy.Rules == nil {
		return nil, errors.New("invalid stored model routing document")
	}
	if err := validateModelRouting(policy); err != nil {
		return nil, errors.New("invalid stored model routing policy: " + err.Error())
	}
	return policy, nil
}

// LoadModelRouting reads the latest persisted policy for the management API,
// which needn't wait for background node sync to recover from a stale save.
// The returned policy is read-only, like CurrentModelRouting.
func LoadModelRouting() (*ModelRoutingPolicy, error) {
	var option Option
	err := DB.Take(&option, &Option{Key: modelRoutingOptionKey}).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CurrentModelRouting(), nil
	}
	if err != nil {
		return nil, err
	}
	if option.Key != modelRoutingOptionKey {
		return nil, errors.New("stored model routing option has a noncanonical key")
	}
	policy, err := parseModelRouting(option.Value)
	if err != nil {
		return nil, err
	}
	publishModelRouting(policy)
	return CurrentModelRouting(), nil
}

// SaveModelRouting uses the document's revision as the expected revision.
// It returns the new, read-only policy only after the database commit succeeds.
func SaveModelRouting(policy ModelRoutingPolicy) (*ModelRoutingPolicy, error) {
	if err := validateModelRouting(&policy); err != nil {
		return nil, err
	}
	if policy.Revision == maxModelRoutingRevision {
		return nil, common.NewMessage("Invalid model routing revision")
	}
	var next *ModelRoutingPolicy
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Create the revision-zero row if needed before locking/reading it.
		// SQLite's first write acquires its writer lock; on MySQL and PostgreSQL
		// the primary key also serializes two concurrent initial inserts.
		anchor := Option{Key: modelRoutingOptionKey, Value: initialModelRoutingDocument}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&anchor).Error; err != nil {
			return err
		}
		var option Option
		if err := lockForUpdate(tx).Take(&option, &Option{Key: modelRoutingOptionKey}).Error; err != nil {
			return err
		}
		if option.Key != modelRoutingOptionKey {
			return errors.New("stored model routing option has a noncanonical key")
		}
		current, err := parseModelRouting(option.Value)
		if err != nil {
			return err
		}
		if current.Revision != policy.Revision {
			return ErrModelRoutingConflict
		}
		if policy.Enabled {
			conflict, err := modelRoutingAliasConflict(tx)
			if err != nil {
				return err
			}
			if conflict {
				return ErrModelRoutingAliasConflict
			}
		}
		next = &ModelRoutingPolicy{Enabled: policy.Enabled, Revision: policy.Revision + 1, Rules: make([]ModelRoutingRule, len(policy.Rules))}
		for i, rule := range policy.Rules {
			rule.Candidates = slices.Clone(rule.Candidates)
			for j := range rule.Candidates {
				rule.Candidates[j].Endpoints = slices.Clone(rule.Candidates[j].Endpoints)
			}
			next.Rules[i] = rule
		}
		encoded, err := common.Marshal(next)
		if err != nil {
			return err
		}
		return tx.Model(&option).Update("value", string(encoded)).Error
	})
	if err != nil {
		return nil, err
	}
	publishModelRouting(next)
	BumpSyncTopic(SyncTopicOptions)
	return next, nil
}

// ModelRoutingAliasConflict reports whether the exact alias is already an
// enabled concrete model in the channel ability inventory.
func ModelRoutingAliasConflict() (bool, error) {
	return modelRoutingAliasConflict(DB)
}

func modelRoutingAliasConflict(db *gorm.DB) (bool, error) {
	query := db.Model(&Ability{}).Where("enabled = ? AND model = ?", true, ModelRoutingAlias)
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		// The default MySQL collations may equate e.g. AUTO with auto.
		query = query.Where("BINARY model = ?", ModelRoutingAlias)
	}
	var found int
	err := query.Select("1").Limit(1).Scan(&found).Error
	return found == 1, err
}

func validateModelRoutingOptionKey(key string) error {
	if strings.EqualFold(strings.TrimSpace(key), modelRoutingOptionKey) {
		return errModelRoutingManagedOption
	}
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		// MySQL can also equate accented or otherwise distinct keys. Compare
		// using the actual option-key column collation even before the routing
		// option exists. The first branch only supplies the column type.
		column := clause.Column{Name: "key"}
		keyType := DB.Model(&Option{}).Select("key").Where("1 = 0")
		var found int
		if err := DB.Raw("SELECT 1 FROM (? UNION ALL SELECT ?) AS reserved_option WHERE ? = ?", keyType, modelRoutingOptionKey, column, key).Scan(&found).Error; err != nil {
			return err
		}
		if found == 1 {
			return errModelRoutingManagedOption
		}
	}
	return nil
}
