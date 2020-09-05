package mock

import (
	"fmt"
	"time"

	"github.com/m-mizutani/deepalert/internal/adaptor"
	"github.com/m-mizutani/deepalert/internal/models"
)

type Repository struct {
	region    string
	tableName string
	data      map[string]map[string]interface{}
}

func NewRepository(region, tableName string) adaptor.Repository {
	return &Repository{
		region:    region,
		tableName: tableName,
		data:      make(map[string]map[string]interface{}),
	}
}

var errCondition = fmt.Errorf("condition error")

func (x *Repository) put(pk, sk string, v interface{}) {
	m, ok := x.data[pk]
	if !ok {
		m = make(map[string]interface{})
		x.data[pk] = m
	}

	m[sk] = v
}

func (x *Repository) get(pk, sk string) interface{} {
	m, ok := x.data[pk]
	if !ok {
		return nil
	}

	return m[sk]
}

func (x *Repository) getAll(pk string) []interface{} {
	m, ok := x.data[pk]
	if !ok {
		return nil
	}

	var out []interface{}
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func (x *Repository) PutAlertEntry(entry *models.AlertEntry, ts time.Time) error {
	v := x.get(entry.PKey, entry.SKey)
	if e, ok := v.(*models.AlertEntry); ok && ts.UTC().Unix() <= e.ExpiresAt {
		return errCondition
	}
	x.put(entry.PKey, entry.SKey, entry)

	return nil
}

func (x *Repository) GetAlertEntry(pk, sk string) (*models.AlertEntry, error) {
	v := x.get(pk, sk)
	if d, ok := v.(*models.AlertEntry); ok {
		return d, nil
	}
	return nil, nil
}

func (x *Repository) PutAlertCache(cache *models.AlertCache) error {
	x.put(cache.PKey, cache.SKey, cache)
	return nil
}

func (x *Repository) GetAlertCaches(pk string) ([]*models.AlertCache, error) {
	var out []*models.AlertCache
	for _, v := range x.getAll(pk) {
		out = append(out, v.(*models.AlertCache))
	}
	return out, nil
}

func (x *Repository) PutReportSectionRecord(record *models.ReportSectionRecord) error {
	x.put(record.PKey, record.SKey, record)
	return nil
}

func (x *Repository) GetReportSection(pk string) ([]*models.ReportSectionRecord, error) {
	var out []*models.ReportSectionRecord
	for _, v := range x.getAll(pk) {
		out = append(out, v.(*models.ReportSectionRecord))
	}
	return out, nil
}

func (x *Repository) PutAttributeCache(attr *models.AttributeCache, ts time.Time) error {
	v := x.get(attr.PKey, attr.SKey)
	if e, ok := v.(*models.AttributeCache); ok && ts.UTC().Unix() <= e.ExpiresAt {
		return errCondition
	}
	x.put(attr.PKey, attr.SKey, attr)

	return nil
}
func (x *Repository) GetAttributeCaches(pk string) ([]*models.AttributeCache, error) {
	var out []*models.AttributeCache
	for _, v := range x.getAll(pk) {
		out = append(out, v.(*models.AttributeCache))
	}
	return out, nil
}

func (x *Repository) IsConditionalCheckErr(err error) bool {
	return err == errCondition
}
