package functions

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/google/uuid"
	"github.com/guregu/dynamo"
	"github.com/pkg/errors"

	"github.com/m-mizutani/deepalert"
)

/*
	DynamoDB Design

	Data models
	- Alert : Generated by a security monitoring device. It has attribute(s).
	- Attribute : Values appeared in an Alert (e.g. IP address, domain name, user name, etc.)
	- Content : A result of attribute inspection by Inspector
	- Report : All results. It consists of Alert(S), Content(S) and a result of Reviewer.


	Keys
	- AlertID : Generated by Alert.Detector, Alert.RuneName and Alert.AlertKey.
	- ReportID : Assigned to unique AlertKey and time range. Same AlertID can have multiple
				ReportID if timestamps of alert are distant from each other.
	- AttrHash: Hashed value of an attribute, generated by all fields of Attribute.

	Primary/secondary key design (in "pk", "sk" field and stored data)
	- alertmap/{AlertID}, fixedkey -> ReportID
	- alert/{ReportID}, cache/{random} -> Alert(s)
	- content/{ReportID}, {AttrHash}/{Random} -> Content(S)
	- attribute/{ReportID}, {AttrHash} -> Attribute (for caching)
*/

type DataStoreService struct {
	tableName  string
	region     string
	table      dynamo.Table
	timeToLive time.Duration
}

func NewDataStoreService(tableName, region string) *DataStoreService {
	db := dynamo.New(session.New(), &aws.Config{Region: aws.String(region)})
	x := DataStoreService{
		tableName:  tableName,
		region:     region,
		table:      db.Table(tableName),
		timeToLive: time.Hour * 3,
	}

	return &x
}

type recordBase struct {
	PKey      string    `dynamo:"pk"`
	SKey      string    `dynamo:"sk"`
	ExpiresAt time.Time `dynamo:"expires_at"`
	CreatedAt time.Time `dynamo:"created_at,omitempty"`
}

// -----------------------------------------------------------
// Control alertEntry to manage AlertID to ReportID mapping
//
type alertEntry struct {
	recordBase
	ReportID deepalert.ReportID `dynamo:"report_id"`
}

func isConditionalCheckErr(err error) bool {
	if ae, ok := err.(awserr.RequestFailure); ok {
		return ae.Code() == "ConditionalCheckFailedException"
	}
	return false
}

func NewReportID() deepalert.ReportID {
	return deepalert.ReportID(uuid.New().String())
}

func (x *DataStoreService) TakeReport(alert deepalert.Alert) (*deepalert.Report, error) {
	fixedKey := "Fixed"
	alertID := alert.AlertID()
	ts := alert.Timestamp
	now := time.Now().UTC()

	cache := alertEntry{
		recordBase: recordBase{
			PKey:      "alertmap/" + alertID,
			SKey:      fixedKey,
			ExpiresAt: ts.Add(time.Hour * 3),
			CreatedAt: now,
		},
		ReportID: NewReportID(),
	}

	if err := x.table.Put(cache).If("(attribute_not_exists(pk) AND attribute_not_exists(sk)) OR expires_at < ?", ts).Run(); err != nil {
		if isConditionalCheckErr(err) {
			var existedEntry alertEntry
			if err := x.table.Get("pk", cache.PKey).Range("sk", dynamo.Equal, cache.SKey).One(&existedEntry); err != nil {
				return nil, errors.Wrapf(err, "Fail to get cached reportID, AlertID=%s", alertID)
			}

			return &deepalert.Report{
				ID:        existedEntry.ReportID,
				Status:    deepalert.StatusMore,
				CreatedAt: existedEntry.CreatedAt,
			}, nil
		}

		return nil, errors.Wrapf(err, "Fail to get cached reportID, AlertID=%s", alertID)
	}

	return &deepalert.Report{
		ID:        cache.ReportID,
		Status:    deepalert.StatusNew,
		CreatedAt: now,
	}, nil
}

// -----------------------------------------------------------
// Control alertCache to manage published alert data
//
type alertCache struct {
	PKey      string    `dynamo:"pk"`
	SKey      string    `dynamo:"sk"`
	AlertData []byte    `dynamo:"alert_data"`
	ExpiresAt time.Time `dynamo:"expires_at"`
}

func toAlertCacheKey(reportID deepalert.ReportID) (string, string) {
	return fmt.Sprintf("alert/%s", reportID), "cache/" + uuid.New().String()
}

func (x *DataStoreService) SaveAlertCache(reportID deepalert.ReportID, alert deepalert.Alert) error {
	raw, err := json.Marshal(alert)
	if err != nil {
		return errors.Wrapf(err, "Fail to marshal alert: %v", alert)
	}

	pk, sk := toAlertCacheKey(reportID)
	cache := alertCache{
		PKey:      pk,
		SKey:      sk,
		AlertData: raw,
		ExpiresAt: alert.Timestamp.Add(x.timeToLive),
	}

	if err := x.table.Put(cache).Run(); err != nil {
		return errors.Wrap(err, "")
	}

	return nil
}

func (x *DataStoreService) FetchAlertCache(reportID deepalert.ReportID) ([]deepalert.Alert, error) {
	pk, _ := toAlertCacheKey(reportID)
	var caches []alertCache
	var alerts []deepalert.Alert

	if err := x.table.Get("pk", pk).All(&caches); err != nil {
		return nil, errors.Wrapf(err, "Fail to retrieve alertCache: %s", reportID)
	}

	for _, cache := range caches {
		var alert deepalert.Alert
		if err := json.Unmarshal(cache.AlertData, &alert); err != nil {
			return nil, errors.Wrapf(err, "Fail to unmarshal alert: %s", string(cache.AlertData))
		}
		alerts = append(alerts, alert)
	}

	return alerts, nil
}

// -----------------------------------------------------------
// Control reportRecord to manage report contents by inspector
//
type reportSectionRecord struct {
	recordBase
	Data []byte `dynamo:"data"`
}

func toReportSectionRecord(reportID deepalert.ReportID, section *deepalert.ReportSection) (string, string) {
	pk := fmt.Sprintf("content/%s", reportID)
	sk := ""
	if section != nil {
		sk = fmt.Sprintf("%s/%s", section.Attribute.Hash(), uuid.New().String())
	}
	return pk, sk
}

func (x *DataStoreService) SaveReportSection(section deepalert.ReportSection) error {
	raw, err := json.Marshal(section)
	if err != nil {
		return errors.Wrapf(err, "Fail to marshal ReportSection: %v", section)
	}

	pk, sk := toReportSectionRecord(section.ReportID, &section)
	record := reportSectionRecord{
		recordBase: recordBase{
			PKey:      pk,
			SKey:      sk,
			ExpiresAt: time.Now().UTC().Add(time.Hour * 24),
		},
		Data: raw,
	}

	if err := x.table.Put(record).Run(); err != nil {
		return errors.Wrap(err, "Fail to put report record")
	}

	return nil
}

func (x *DataStoreService) FetchReportSection(reportID deepalert.ReportID) ([]deepalert.ReportSection, error) {
	var records []reportSectionRecord
	pk, _ := toReportSectionRecord(reportID, nil)

	if err := x.table.Get("pk", pk).All(&records); err != nil {
		return nil, errors.Wrap(err, "Fail to fetch report records")
	}

	var sections []deepalert.ReportSection
	for _, record := range records {
		var section deepalert.ReportSection
		if err := json.Unmarshal(record.Data, &section); err != nil {
			return nil, errors.Wrapf(err, "Fail to unmarshal report content: %v %s", record, string(record.Data))
		}

		sections = append(sections, section)
	}

	return sections, nil
}

// -----------------------------------------------------------
// Control attribute cache to prevent duplicated invocation of Inspector with same attribute
//
type attributeCache struct {
	recordBase
	Timestamp time.Time `dynamo:"timestamp"`
	AttrKey   string    `dynamo:"attr_key"`
	AttrType  string    `dynamo:"attr_type"`
	AttrValue string    `dynamo:"attr_value"`
}

// PutAttributeCache puts attributeCache to DB and returns true. If the attribute alrady exists,
// it returns false.
func (x *DataStoreService) PutAttributeCache(reportID deepalert.ReportID, attr deepalert.Attribute) (bool, error) {
	now := time.Now().UTC()
	var ts time.Time
	if attr.Timestamp != nil {
		ts = *attr.Timestamp
	} else {
		ts = now
	}

	cache := attributeCache{
		recordBase: recordBase{
			PKey:      "attribute/" + string(reportID),
			SKey:      attr.Hash(),
			ExpiresAt: now.Add(time.Hour * 3),
		},
		Timestamp: ts,
		AttrKey:   attr.Key,
		AttrType:  string(attr.Type),
		AttrValue: attr.Value,
	}

	if err := x.table.Put(cache).If("(attribute_not_exists(pk) AND attribute_not_exists(sk)) OR expires_at < ?", now).Run(); err != nil {
		if isConditionalCheckErr(err) {
			// The attribute already exists
			return false, nil
		}

		return false, errors.Wrapf(err, "Fail to put attr cache reportID=%s, %v", reportID, attr)
	}

	return true, nil
}
