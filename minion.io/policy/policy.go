package policy

/*************************************
// TODO: break up file into modules
// Nextensio interface for Opa Rego library to provide policy based services covering
// application access authorization, Agent authorization, Connector authorization,
// routing, tracing, etc.
// This code will be compiled together with the minion code running in a service pod.
// The minion code will first call nxtOpaInit() to set things up. After that, it will
// call an API specific to the authorization (or whatever) policy check required.
// Some APIs are to be called in ingress service pod, some in egress service pod.
// Common for every pod:
// NxtOpaInit() - to be called once for initialization before any other API calls
// Apod APIs:
//     func NxtGetUsrAttr(userid string) (string, bool)
//     func NxtUsrLeave(userid string)
//     func NxtUsrAllowed(userid string) bool
//     func NxtRouteLookup(userid string, host string, ...)
//     func NxtTraceLookup(userattr string)
// Cpod APIs:
//     func NxtAccessOk(bundleid string, userattr string) bool
//
*************************************/

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"sync"
	"time"

	"github.com/open-policy-agent/opa/rego"
	common "gitlab.com/nextensio/common/go"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

//
//--------------------------------Data Structures, Variables, etc ----------------------------------

const maxOpaUseCases = 10 // we currently have 5; stats makes it 6
const maxMongoColls = 10  // assume max 10 MongoDB tenant collections, currently 6+1
const maxUsers = 10000    // max per tenant

/*****************************
// MongoDB database and collections
*****************************/
var tenantDB *mongo.Database

const nxtMongoDB = "NxtDB" // default DB name prior to per-tenant DBs
const policyCollection = "NxtPolicies"
const userInfoCollection = "NxtUsers"
const userAttrCollection = "NxtUserAttr"
const connInfoCollection = "NxtApps"
const appAttrCollection = "NxtAppAttr"
const hostAttrCollection = "NxtHostAttr"
const traceReqCollection = "NxtTraceRequests"
const statsReqCollection = "" // We don't need a reference data collection

const HDRKEY = "Header"

// These are key names that should be used in the OPA Rego policies
const kmajver = "maj_ver" // Collection major version
const kminver = "min_ver" // Collection minor version
const kbid = "bid"        // App-bundle ID from AppInfo and AppAttr collections
const kuser = "uid"       // User ID from UserInfo and UserAttr collections
const khost = "host"      // Host ID from HostAttr collection
// These are key names that anchor a group of attributes for use in OPA Rego policies
const kbundleattrs = "bundles"    // Anchor for all refdata app-bundle attributes
const khostattrs = "hosts"        // Anchor for all refdata host attributes
const kuserattrs = "user"         // Anchor for input user attributes in routing
const ktracereq = "tracerequests" // Anchor for all trace requests
//const khostrouteattrs = "routeattrs" // Anchor for attributes per route tag for a host

// Common struct for all policies
// Can't seem to add a Rego policy via mongoshell, hence the hack to also
// provide a filename that is then used to read the policy from a local file.
type Policy struct {
	PolicyId string `json:"pid" bson:"_id"`
	ChangeBy string `json:"changeby" bson:"changeby"`
	ChangeAt string `json:"changeat" bson:"changeat"`
	Majver   int    `json:"majver" bson:"majver"` // major version
	Minver   int    `json:"minver" bson:"minver"` // minor version
	Fname    string `json:"fname" bson:"fname"`   // rego policy filename
	Rego     []rune `json:"rego" bson:"rego"`     // rego policy
}

// Header document for a data collection so that the versions and tenant are not
// replicated in every document. The header can be read upfront to get the version
// info for the collection from a single document.
// Ensure this always matches with the definition in the controller repo.
type DataHdr struct {
	ID       string `bson:"_id" json:"ID"`
	Majver   int    `bson:"majver" json:"majver"`
	Minver   int    `bson:"minver" json:"minver"`
	ChangeBy string `bson:"changeby" json:"changeby"`
	ChangeAt string `bson:"changeat" json:"changeat"`
}

// Data object to track every use case
type QState struct {
	NewVer  bool                   // new version of policy or refdata
	NewPol  bool                   // new version of rego poloicy
	NewData bool                   // new version of reference data
	WrVer   bool                   // write versions to file (for testing infra)
	QError  bool                   // error in query state
	Qry     string                 // the OPA Rego query
	QUCase  string                 // query use case
	PrepQry rego.PreparedEvalQuery // compiled query
	PolType string                 // key for policy
	PStruct Policy                 // Policy struct
	RegoPol []byte                 // rego policy
	LDir    string                 // load directory for OPA
	DColl   string                 // name of reference data collection
	HdrKey  string                 // Header doc keys for reference data collections
	RefHdr  DataHdr                // reference data header doc
	RefData []byte                 // reference data
}

// Info for test cases
type TState struct { // For testing
	Count int         // Count of keys
	Keys  [500]string // Keys - Bids or Hosts or ...
}

type UserInfo struct {
	Userid  string
	Cluster string
	Podname string
}

var QStateMap = make(map[string]*QState, maxOpaUseCases) // indexed by opaUseCases
var TStateMap = make(map[string]*TState, maxOpaUseCases)

var opaUseCases = []string{
	"AgentAuthz",
	"ConnAuthz",
	"AppAccess",
	"RoutePol",
	"TracePol",
	"StatsPol",
}
var initUseCase = []int{ // 0 = disable; 1 = enable
	0, // Agent authorization
	0, // Connector authorization
	1, // App-bundle access authorization
	1, // Host routing
	1, // Tracing
	1, // Stats
}

// policyType - Policy doc Keys
var policyType = []string{
	"AgentPolicy",
	"ConnPolicy",
	"AccessPolicy",
	"RoutePolicy",
	"TracePolicy",
	"StatsPolicy",
}
var opaQuery = []string{ // Result document from policy
	"data.app.access.allow",
	"data.app.access.allow",
	"data.app.access.allow",
	"data.user.routing.route_tag",
	"data.user.tracing.request",
	"data.user.stats.attributes",
}

// These need to be created via the Dockerfiles for the policy and refdata files
// required at run-time by OPA
var loadDir = []string{
	"authz/agent-authz",
	"authz/conn-authz",
	"authz/app-access",
	"authz/routing",
	"authz/tracing",
	"authz/stats",
}
var DColls = []string{
	userInfoCollection,
	connInfoCollection,
	appAttrCollection,
	hostAttrCollection,
	traceReqCollection,
	statsReqCollection,
}

var initExit = make(chan bool, 1)
var initDone bool
var procStarted bool
var mongoCheck bool
var mongoFailures int
var mongoInitDone bool
var mongoDBAddr string
var tenant string
var nxtPod string
var nxtGw string
var slog *zap.Logger
var st, sp string // for tenant, gateway:pod

var CollMap map[string]*mongo.Collection
var mongoClient *mongo.Client
var nxtMongoDBName string // set to nxtMongoDB (legacy) or a unique name per tenant going forward

func NxtOpaInit(namespace string, pod string, gateway string, mongouri string, sl *zap.Logger) error {
	return nxtOpaInit(namespace, pod, gateway, mongouri, sl)
}

// Check on Apod for user and Cpod for connector
func NxtUsrAllowed(which string, info UserInfo) bool {
	if !initDone || !mongoInitDone {
		return false
	}
	if info.Podname != "" && info.Podname != nxtPod {
		nxtLogDebug("UsrAllow", fmt.Sprintf("Pod mismatch for %s, pod=%s", info.Userid, info.Podname))
		return false
	}
	return true
}

// Purge a user's attributes when last agent of that user leaves
func NxtUsrLeave(which string, userid string) {
	if !initDone {
		return
	}
	if which == "agent" {
		// Don't check mongoInitDone as we purge the data in local cache
		nxtPurgeUserAttrJSON(userid)
	} else {
		nxtPurgeAppAttrJSON(userid)
	}
}

// Get user attributes on Apod. Passed to Cpod via flow header.
func NxtGetUsrAttr(which string, userid string, extattr string) string {
	var uajson []byte
	var uabson, extbson primitive.M
	var err error
	// Don't check mongoInitDone as we may have the data in local cache
	// The call handles the case where mongo init not done and data not in local cache
	if extattr != "" {
		if err = json.Unmarshal([]byte(extattr), &extbson); err != nil {
			nxtLogError("AgentAttributes",
				fmt.Sprintf("JSON unmarshal error (%v) for "+extattr, err))
			extattr = ""
		}
	}
	if !initDone {
		return extattr
	}
	if which == "agent" {
		uajson, _ = nxtGetUserAttr(userid)
	} else {
		uajson, _ = nxtGetAppAttr(userid)
	}
	// NOTE: Since Go returns a pointer to a map stucture and not the structure itself,
	// the code is changed to return a byte array so we can unmarshal into our own map
	// struct for modifications. Helps avoid the possibility of inadvertently modifying
	// the underlying common map structure in a multiple threads/multiple users case.
	if err = json.Unmarshal(uajson, &uabson); err != nil {
		nxtLogError("UserAttributes", fmt.Sprintf("JSON unmarshal error - %v", err))
		return extattr
	}
	for k, v := range extbson {
		uabson[k] = v
	}
	return nxtConvertToJSONString(uabson)
}

// Access policy is run only on Cpod
func NxtAccessOk(which string, bundleid string, userattr string) bool {
	if !initDone {
		return false
	}
	if which == "agent" {
		return true
	}
	return nxtEvalAppAccessAuthz(opaUseCases[2], userattr, bundleid)
}

// Route policy is run only on Apod
func NxtRouteLookup(which string, uid string, usrattr string, host string) string {
	if !initDone || !mongoInitDone {
		return ""
	}
	return nxtEvalUserRouting(which, opaUseCases[3], uid, usrattr, host)
}

// Trace policy is run only on Apod
func NxtTraceLookup(which string, uattr string) string {
	if !initDone || !mongoInitDone {
		return "no"
	}
	if which != "agent" {
		return "no"
	}
	return nxtEvalUserTracing(opaUseCases[4], uattr)
}

// Stats policy is run only on Apod. It returns a list (format TBD) of user attributes
// selected by tenant as stats dimensions
func NxtStatsAttributes(which string) string {
	deflist := "{\"exclude\": [\"uid\", \"maj_ver\", \"min_ver\", \"_hostname\", \"_model\", \"_osMinor\", \"_osPatch\", \"_osName\"]}"

	if !initDone || !mongoInitDone {
		return deflist
	}
	sattrs := nxtEvalUserStats(opaUseCases[5])
	if sattrs == "" {
		return deflist
	}
	return sattrs
}

// For now, this function tests access for a number of users with each app bundle.
// In production, this will monitor for DB updates and pull in any modified documents
// to reinitialize any OPA stuff
func nxtOpaProcess(ctx context.Context) int {
	var cs *mongo.ChangeStream
	var err error

	// Set up watch for updates to minver field of any document
	// in any of the tenantDB collections
	pipeline := mongo.Pipeline{bson.D{{"$match", bson.D{{"$and",
		bson.A{
			bson.D{{"updateDescription.updatedFields.minver", bson.D{
				{"$gt", 0},
			}}},
			bson.D{{"operationType", "update"}}}}},
	}}}

	// First, wait until the code exits nxtOpaInit()
	select {
	case <-initExit:
		nxtLogDebug("OpaProcess", "InitExit set- init done")
	}

	// Loop until all initialization is done in case nxtOpaInit() failed
	nxtOpaProcessInitCheck(ctx)

	// Finally, loop forever
	// 1. checking if mongoDB connection is still alive if any mongoDB access failed
	// 2. to reinitialize mongoDB connection if it has failed
	// 3. checking for and processing any DB collection version changes
	nxtLogDebug("OpaProcess", fmt.Sprintf("initDone=%v, mongoInitDone=%v, mongoCheck=%v",
		initDone, mongoInitDone, mongoCheck))
	for {
		// sleep(1 sec)
		time.Sleep(1 * 1000 * time.Millisecond)

		nxtOpaProcessMongoCheck(ctx)

		if !mongoInitDone {
			// Skip any further code if mongoDB not accessible
			continue
		}

		// Watch the tenantDB. Retry for 5 times before bailing out of watch
		for retries := 1; retries <= 5; retries++ {
			cs, err = tenantDB.Watch(context.TODO(), pipeline)
			if err != nil {
				nxtLogError(nxtMongoDBName, fmt.Sprintf("Not able to register for change notification [err:%s]. Retrying...%d", err, retries))
				time.Sleep(1 * time.Second)
			} else {
				nxtLogDebug(nxtMongoDBName, fmt.Sprintf("Database watch started."))
				break
			}
		}
		if err != nil {
			// Failed to register watch; continue on mongoCheck
			nxtLogError(nxtMongoDBName, fmt.Sprintf("DB watch register error - %v", err))
			continue
		}

		// Check all collections once after setting up watch before waiting for
		// change notifications. Better to process a change twice rather than miss
		// a change.
		for i, ucase := range opaUseCases {
			if initUseCase[i] > 0 {
				nxtSetupUseCase(ctx, i, ucase)
			}
		}
		nxtProcessUserAttrChanges(ctx)
		nxtProcessAppAttrChanges(ctx)
		if usrAttrWrVer || appAttrWrVer ||
			QStateMap[opaUseCases[2]].WrVer ||
			QStateMap[opaUseCases[3]].WrVer {
			nxtWriteAttrVersions()
		}

		// Wait for change notification from tenant DB for processing
		// Note that we are only looking for changes to minver field in the header
		// of collections relevant here.
		for cs.Next(context.TODO()) {
			var changeEvent bson.M

			err = cs.Decode(&changeEvent)
			if err != nil {
				// We don't expect the decode to fail. Since, we missed to decode this event,
				// lets close watch and rewatch again.
				cs.Close(context.TODO())
				break
			}

			// Get the collection info for this event first
			ns := changeEvent["ns"].(primitive.M)
			coll := ns["coll"].(string)
			nxtLogDebug(nxtMongoDBName, fmt.Sprintf(" DB ChangeEvent: Coll:%s Event:%s\n", coll, changeEvent["operationType"]))

			for i, ucase := range opaUseCases {
				if initUseCase[i] > 0 {
					// If use case is active, check if Policy collection or use case
					// reference data collection has changed
					if (coll == "NxtPolicies") || (QStateMap[ucase].DColl == coll) {
						nxtSetupUseCase(ctx, i, ucase)
					}
				}
			}
			if coll == "NxtUserAttr" {
				nxtProcessUserAttrChanges(ctx)

			}
			if coll == "NxtAppAttr" {
				nxtProcessAppAttrChanges(ctx)
			}

			if usrAttrWrVer || appAttrWrVer ||
				QStateMap[opaUseCases[2]].WrVer ||
				QStateMap[opaUseCases[3]].WrVer {
				nxtWriteAttrVersions()
			}

		}
		nxtLogDebug(nxtMongoDBName, fmt.Sprintf("Watch MongoDB change notification disconnected\n"))
	}
}

func nxtOpaProcessInitCheck(ctx context.Context) {
	var err error
	nxtLogDebug("OpaProcessInitCheck", fmt.Sprintf("initDone=%v, mongoInitDone=%v, mongoCheck=%v",
		initDone, mongoInitDone, mongoCheck))
	for {
		if initDone {
			break
		}
		time.Sleep(2 * 1000 * time.Millisecond)
		if !mongoInitDone {
			nxtLogDebug("OpaProcess", "Calling mongoDB init as it wasn't done")
			mongoClient, err = nxtMongoDBInit(ctx, tenant, mongoDBAddr)
			if err != nil {
				nxtLogError(nxtMongoDBName, fmt.Sprintf("DB init retry error - %v", err))
				continue
			}
			mongoInitDone = true
			nxtOpaUseCaseInit(ctx)
		}
	}
}

func nxtOpaProcessMongoCheck(ctx context.Context) {
	var err error
	if !mongoInitDone {
		// No connection to mongoDB. Try to reconnect.
		nxtLogDebug("OpaProcess", "Calling mongoDB init - mongoInitDone false")
		mongoClient, err = nxtMongoDBInit(ctx, tenant, mongoDBAddr)
		if err != nil {
			nxtLogError(nxtMongoDBName, fmt.Sprintf("DB init retry error - %v", err))
			return
		}
		mongoInitDone = true
		mongoCheck = false
	}
	if mongoCheck {
		// There has been a mongoDB access failure.
		// Do a ping and see if DB is accessible. If not, reinit.
		err = mongoClient.Ping(ctx, nil)
		if err != nil {
			_ = mongoClient.Disconnect(ctx)
			mongoClient, err = nxtMongoDBInit(ctx, tenant, mongoDBAddr)
			if err == nil {
				// We have a new mongoDB connection.
				mongoInitDone = true
				mongoCheck = false
				mongoFailures = 0
			} else {
				// Failed to reconnect to mongoDB. Keep retrying.
				nxtLogError(nxtMongoDBName,
					fmt.Sprintf("Failed to reinit DB after ping failure - %v",
						err))
				nxtLogError(nxtMongoDBName,
					fmt.Sprintf("Total %d DB access failures", mongoFailures))
				mongoInitDone = false
			}
		} else {
			// Ping to mongoDB works fine. Reset flag.
			mongoCheck = false
			nxtLogDebug(nxtMongoDBName,
				fmt.Sprintf("%d DB access failures seen but ping is fine",
					mongoFailures))
		}
	}
}

// API to init nxt OPA interface
func nxtOpaInit(ns string, pod string, gateway string, mongouri string, sl *zap.Logger) error {
	defer func() {
		initExit <- true
	}()

	var err error

	if procStarted {
		return nil
	}

	ctx := context.Background()
	slog = sl
	tenant = common.NamespaceToTenant(ns)
	nxtPod = pod
	nxtGw = gateway
	st = "Tenant: " + tenant
	sp = "Pod: " + nxtGw + ":" + nxtPod

	nxtMongoDBName = nxtGetTenantDBName(tenant)
	mongoDBAddr = mongouri

	go nxtOpaProcess(ctx)
	procStarted = true

	mongoClient, err = nxtMongoDBInit(ctx, tenant, mongouri)
	if err != nil {
		nxtLogError(nxtMongoDBName, fmt.Sprintf("DB init error - %v", err))
		return err
	}
	mongoInitDone = true

	nxtOpaUseCaseInit(ctx)
	nxtLogDebug("OpaInit", "Exiting OpaInit()")
	return nil
}

func nxtOpaUseCaseInit(ctx context.Context) {

	// Initialize for each OPA use case as required. Initialization involves reading the
	// associated policy and reference data document or collection, ensuring their major
	// version matches, and if so, loading them into the load directory before creating
	// the query object and preparing the query for evaluation.
	for i, ucase := range opaUseCases {
		nxtCreateOpaUseCase(i, ucase)
		if initUseCase[i] > 0 { // Initialize now in Init function
			nxtSetupUseCase(ctx, i, ucase)
		}
	}
	// Read header document for user attributes collection.
	// Set up cache for user attributes.
	// Then read the extended (runtime) attributes spec doc
	userAttr = make(map[string]usrCache, maxUsers)
	appAttr = make(map[string]usrCache)
	usrAttrHdr = nxtReadUserAttrHdr(ctx)
	appAttrHdr = nxtReadAppAttrHdr(ctx)

	nxtWriteAttrVersions()
	initDone = true
}

// Do everything needed to set up mongoDB access
func nxtMongoDBInit(ctx context.Context, ns string, mURI string) (*mongo.Client, error) {

	cl, err := nxtMongoConnect(ctx, ns, mURI)
	if err != nil {
		return nil, err
	}

	CollMap = make(map[string]*mongo.Collection, maxMongoColls)
	tenantDB = cl.Database(nxtMongoDBName)
	nxtLogDebug(nxtMongoDBName, "The DB being used")

	// Required on both apod and cpod
	CollMap[policyCollection] = tenantDB.Collection(policyCollection)
	// Required on cpod only
	CollMap[connInfoCollection] = tenantDB.Collection(connInfoCollection)
	CollMap[appAttrCollection] = tenantDB.Collection(appAttrCollection)
	// Required on apod only
	CollMap[userInfoCollection] = tenantDB.Collection(userInfoCollection)
	CollMap[hostAttrCollection] = tenantDB.Collection(hostAttrCollection)
	CollMap[traceReqCollection] = tenantDB.Collection(traceReqCollection)
	// Required on apod. Required on cpod for testing app-access authz
	CollMap[userAttrCollection] = tenantDB.Collection(userAttrCollection)

	return cl, nil
}

func nxtMongoConnect(ctx context.Context, ns string, mURI string) (*mongo.Client, error) {
	var err error

	// Set client options
	mongoclientOptions := options.Client().ApplyURI(mURI)

	// Connection to mongoDB is critical, so be persistent and retry in case of failure.
	// We are seeing random mongodb connect failures due to DNS resolution latency.
	for rtry1 := 0; rtry1 < 3; rtry1 = rtry1 + 1 {
		// Connect to MongoDB
		cl, err1 := mongo.Connect(ctx, mongoclientOptions)
		err = err1
		if err == nil { // Connected
			var err2 error
			for rtry2 := 0; rtry2 < 3; rtry2 = rtry2 + 1 {
				// Check the connection
				err2 = cl.Ping(ctx, nil)
				if err2 == nil {
					return cl, nil // Success
				}
				time.Sleep(1 * 1000 * time.Millisecond)
			}
			_ = cl.Disconnect(ctx)
			nxtLogError(nxtMongoDBName, fmt.Sprintf("Connection closed. DB ping failure - %v", err2))
		} else {
			nxtLogError(nxtMongoDBName, fmt.Sprintf("DB connect failure - %v", err))
		}
		time.Sleep(2 * 1000 * time.Millisecond)
	}
	return nil, err
}

func nxtGetTenantDBName(tenant string) string {
	return ("Nxt-" + tenant + "-DB")
}

func nxtMongoError() {
	mongoCheck = true
	mongoFailures++
}

// Create use case for each query type
func nxtCreateOpaUseCase(i int, ucase string) {
	var NewState QState
	var NewTS TState

	NewState.QUCase = ucase
	NewState.PolType = policyType[i]
	NewState.LDir = loadDir[i]
	NewState.Qry = opaQuery[i]
	NewState.HdrKey = HDRKEY
	NewState.DColl = DColls[i]
	NewState.QError = true
	QStateMap[ucase] = &NewState
	TStateMap[ucase] = &NewTS
	msg := "Use case created for " + policyType[i] + " with Refdata as " + DColls[i]
	nxtLogDebug(ucase, msg)
}

// Initialize and set up each use case for using OPA
func nxtSetupUseCase(ctx context.Context, i int, ucase string) {

	// read policy document and store it in QStateMap if version is newer
	nxtReadPolicyDocument(ctx, ucase, policyType[i])

	// read associated data collection and see if a newer version is available
	if nxtReadRefDataHdr(ctx, ucase) {
		nxtReadRefDataDoc(ctx, ucase)
	}
	nxtCheckUseCaseLoading(ctx, i, ucase)
}

func nxtCheckUseCaseLoading(ctx context.Context, i int, ucase string) {
	// check if Data load directory needs to be updated. If yes, create query
	// if not created and prepare query for evaluation with new policy/refdata
	qs := QStateMap[ucase]
	if qs.NewVer {
		// A new policy and/or new data collection is available
		// If their major versions match, set up the Data load directory
		if qs.PStruct.Majver == qs.RefHdr.Majver {
			nxtPrimeLoadDir(ucase)
			qs.NewVer = false
			QryObj := nxtCreateOpaQry(qs.Qry, qs.LDir)
			qs.PrepQry, qs.QError = nxtPrepOpaQry(ctx, ucase, QryObj)
		}
	}
}

//-------------------------------Policy functions-----------------------------------
// Read policy of specified type.
func nxtReadPolicyDocument(ctx context.Context, usecase string, ptype string) {
	var policy Policy

	// Read specific policy by specifying "_id" = ptype
	err := CollMap[policyCollection].FindOne(ctx, bson.M{"_id": ptype}).Decode(&policy)
	if err != nil {
		nxtLogError(usecase, fmt.Sprintf("Failed to find %s, error - %v", ptype, err))
		nxtMongoError()
		return
	}
	qs := QStateMap[usecase]
	if (policy.Majver > qs.PStruct.Majver) || (policy.Minver > qs.PStruct.Minver) {
		// New policy. Store it in QStateMap
		qs.PStruct = policy
		qs.NewVer = true
		qs.NewPol = true
		qs.WrVer = true
		if qs.PStruct.Fname != "" {
			// Read policy from local file, not mongoDB
			qs.RegoPol = nxtReadPolicyLocalFile(usecase)
		} else {
			qs.RegoPol = []byte(string(qs.PStruct.Rego))
		}
		msg := fmt.Sprintf("Loaded version %d of ", policy.Minver)
		msg += ptype + " changed by " + policy.ChangeBy + " at " + policy.ChangeAt
		nxtLogDebug(usecase, msg)
	}
}

// Read Policy file from local file and return bytes read
func nxtReadPolicyLocalFile(ucase string) []byte {
	// Read policy file and return the data read
	ps := QStateMap[ucase].PStruct
	bs, err := ioutil.ReadFile(ps.Fname)
	if err != nil {
		tstr := fmt.Sprintf("Local policy file %s read failure - %v", ps.Fname, err)
		nxtLogError(ucase, tstr)
	}
	return bs
}

//--------------------------------Reference Data funtions-------------------------------------
// Read header and match versions to see if there's a newer version in the DB.
// If there's a newer version, read the collection and set NewVer to true so that
// the query can be prepared for evaluation again with the new reference data
func nxtReadRefDataHdr(ctx context.Context, ucase string) bool {

	// read version document for data collection
	var hdr DataHdr
	qs := QStateMap[ucase]
	coll := qs.DColl
	if coll == "" {
		// No reference data collection. Fake it
		qs.RefHdr.Majver = qs.PStruct.Majver
		qs.RefHdr.Minver = qs.PStruct.Minver
		return false
	}
	err := CollMap[coll].FindOne(ctx, bson.M{"_id": HDRKEY}).Decode(&hdr)
	if err != nil {
		nxtLogError(ucase, fmt.Sprintf("Failed to find %s %s doc - %v", coll, qs.HdrKey, err))
		nxtMongoError()
		return false
	}
	// If data collection majver < policy document majver, ignore data collection and return
	if hdr.Majver < qs.PStruct.Majver {
		return false
	}
	// data collection majver >= policy document majver
	// if data collection majver or minver is newer than current version,
	// read entire data collection and store it in QStateMap
	if (hdr.Majver > qs.RefHdr.Majver) || (hdr.Minver > qs.RefHdr.Minver) {
		qs.RefHdr = hdr
		qs.NewVer = true
		qs.NewData = true
		qs.WrVer = true
		msg := fmt.Sprintf("Found version %d of ", hdr.Minver)
		msg += coll + " changed by " + hdr.ChangeBy + " at " + hdr.ChangeAt
		nxtLogDebug(ucase, msg)
		return true
	}
	return false
}

// Read refdata from mongoDB collection. All data documents are read and combined
// into a JSON string for feeding into OPA
func nxtReadRefDataDoc(ctx context.Context, ucase string) {

	switch ucase {
	case opaUseCases[0]:
		return
	case opaUseCases[1]:
		return
	case opaUseCases[2]:
		// { "bundles": [ {"bid": "id1", ...}, {"bid": "id2", ...} ] }
		QStateMap[ucase].RefData = nxtCreateRefDataDoc(ctx, ucase, kbid, "{ \""+kbundleattrs+"\":  [")
		return
	case opaUseCases[3]:
		// { "hosts": [
		//       {"host": "host1", ... "routeattrs": [{"tag": "v1", ...}, {"tag": "v2", ...}, {"tag": ...}]},
		//       {"host": "host2", ... "routeattrs": [{"tag": "v3", ...}, {"tag": "v4", ...}]}
		//            ]
		// }
		QStateMap[ucase].RefData = nxtCreateRefDataDoc(ctx, ucase, khost, "{ \""+khostattrs+"\":  [")
		return
	case opaUseCases[4]:
		// { "tracerequests": [ {"traceid": "id1", ...}, {"traceid": "id2", ...}, {"traceid": "id3", ...} ] }
		QStateMap[ucase].RefData = nxtCreateRefDataDoc(ctx, ucase, "traceid", "{ \""+ktracereq+"\":  [")
		return
	}
}

//-----------------------------User Attributes Caching Functions------------------------------

// Cache of user attributes for all active users. Cache is updated whenever
// the collection version changes. Cache entries are purged when a user disconnects.
// Changed from a primitive.M map structure to a byte array so callers can unmarshal
// into their own map structure to modify. Go passes a pointer to the map structure
// which can't be used to modify the map in a multi-threaded environment.
type usrCache struct {
	uajson []byte
}

var userAttr map[string]usrCache
var userAttrLock sync.Mutex
var usrAttrHdr DataHdr
var usrAttrWrVer bool

// If new version of user attribues collection is available, read the
// attributes spec doc and update the cache for active users
func nxtProcessUserAttrChanges(ctx context.Context) {
	tmphdr := nxtReadUserAttrHdr(ctx)
	usrAttrHdr = tmphdr
	usrAttrWrVer = true // for testing infra
	nxtUpdateUserAttrCache()
}

// Read header doc from user attributes collection to get version info
func nxtReadUserAttrHdr(ctx context.Context) DataHdr {
	// read header document for user attr collection used as input
	var uahdr DataHdr
	var errhdr = DataHdr{ID: HDRKEY, Majver: 0, Minver: 0}

	coll := CollMap[userAttrCollection]
	err := coll.FindOne(ctx, bson.M{"_id": HDRKEY}).Decode(&uahdr)
	if err != nil {
		nxtLogError(HDRKEY, fmt.Sprintf("Failed to find user attr header doc - %v", err))
		nxtMongoError()
		return errhdr
	}
	return uahdr
}

func loadUserAttrFromDB(uuid string) ([]byte, bool) {
	var uaC usrCache

	ua, ok := nxtReadUserAttrDB(uuid)
	if ok {
		// Cache json form of doc read from DB
		userAttrLock.Lock()
		uaC.uajson = ua
		userAttr[uuid] = uaC
		userAttrLock.Unlock()
		return ua, ok
	}
	return []byte(""), false
}

// Read one user's attr data from mongoDB collection and return it together
// with the json version with header info added. Called when user connects to service pod.
func nxtGetUserAttr(uuid string) ([]byte, bool) {
	uajson, ok := nxtReadUserAttrCache(uuid)
	if ok {
		return uajson, ok // cached version
	}

	return loadUserAttrFromDB(uuid)
}

// Read a user attribute doc from local cache
func nxtReadUserAttrCache(uuid string) ([]byte, bool) {
	userAttrLock.Lock()
	defer userAttrLock.Unlock()

	// Check in cache if user's attributes exist. If yes, return value.
	uaC, ok := userAttr[uuid]
	if ok {
		//nxtLogDebug(uuid, "Retrieved attributes for user from local cache")
		return uaC.uajson, true
	}
	//nxtLogDebug(uuid, "Failed to find attributes for user in local cache")
	return []byte(""), false
}

// Read a user attribute doc from the DB and add header document info
func nxtReadUserAttrDB(uuid string) ([]byte, bool) {
	var usera bson.M

	if !mongoInitDone {
		return []byte(""), false
	}

	ctx := context.Background()

	// Read user attributes from DB, cache json version, and return it
	coll := CollMap[userAttrCollection]
	err := coll.FindOne(ctx, bson.M{"_id": uuid}).Decode(&usera)
	if err != nil {
		nxtLogError(uuid, fmt.Sprintf("Failed to find attributes doc for user - %v", err))
		nxtMongoError()
		return []byte(""), false
	}
	usera = nxtFixupAttrID(usera, kuser)
	usera = nxtAddVerToDoc(usera, usrAttrHdr)
	return nxtConvertToJSONBytes(usera), true
}

// Remove user attributes for a user on disconnect
func nxtPurgeUserAttrJSON(uuid string) {
	userAttrLock.Lock()
	defer userAttrLock.Unlock()
	delete(userAttr, uuid)
}

// Update cache because version info changed for collection
func nxtUpdateUserAttrCache() {
	var uaC usrCache

	userAttrLock.Lock()

	for id, _ := range userAttr {
		ua, _ := nxtReadUserAttrDB(id)
		uaC.uajson = ua
		userAttr[id] = uaC
	}

	userAttrLock.Unlock()
	nxtLogDebug("UserAttrCache", fmt.Sprintf("Updated %v entries in local cache", len(userAttr)))
}

//-----------------------------Bundle Attributes Caching Functions-----------------------------

var appAttr map[string]usrCache
var appAttrLock sync.Mutex
var appAttrHdr DataHdr
var appAttrWrVer bool

// If new version of app attribues collection is available, read the
// attributes docs and update the cache for active apps
func nxtProcessAppAttrChanges(ctx context.Context) {
	tmphdr := nxtReadAppAttrHdr(ctx)
	appAttrHdr = tmphdr
	appAttrWrVer = true // for testing infra
	nxtUpdateAppAttrCache()
}

// Read header doc from app attributes collection to get version info
func nxtReadAppAttrHdr(ctx context.Context) DataHdr {
	// read header document for app attr collection used as input
	var apphdr DataHdr
	var errhdr = DataHdr{ID: HDRKEY, Majver: 0, Minver: 0}

	coll := CollMap[appAttrCollection]
	err := coll.FindOne(ctx, bson.M{"_id": HDRKEY}).Decode(&apphdr)
	if err != nil {
		nxtLogError(HDRKEY, fmt.Sprintf("Failed to find app attr header doc - %v", err))
		nxtMongoError()
		return errhdr
	}
	return apphdr
}

func loadAppAttrFromDB(uuid string) ([]byte, bool) {
	var appC usrCache

	appa, ok := nxtReadAppAttrDB(uuid)
	if ok {
		// Cache doc read from DB
		appAttrLock.Lock()
		appC.uajson = appa
		appAttr[uuid] = appC
		appAttrLock.Unlock()
		return appa, ok
	}
	return []byte(""), false
}

// Read one app's attr data from mongoDB collection and return it together
// with the json version with header info added. Called when app connects to service pod.
func nxtGetAppAttr(uuid string) ([]byte, bool) {
	appajson, ok := nxtReadAppAttrCache(uuid)
	if ok {
		return appajson, ok // cached version
	}

	return loadAppAttrFromDB(uuid)
}

// Read an app attribute doc from local cache
func nxtReadAppAttrCache(uuid string) ([]byte, bool) {
	appAttrLock.Lock()
	defer appAttrLock.Unlock()

	appC, ok := appAttr[uuid]
	if ok {
		return appC.uajson, true
	}
	return []byte(""), false
}

// Read an app attribute doc from the DB and add header document info
func nxtReadAppAttrDB(appid string) ([]byte, bool) {
	var appa bson.M

	if !mongoInitDone {
		return []byte(""), false
	}

	ctx := context.Background()

	// Read app attributes from DB, cache json version, and return it
	coll := CollMap[appAttrCollection]
	err := coll.FindOne(ctx, bson.M{"_id": appid}).Decode(&appa)
	if err != nil {
		nxtLogError(appid, fmt.Sprintf("Failed to find attributes doc for app - %v", err))
		nxtMongoError()
		return []byte(""), false
	}
	appa = nxtFixupAttrID(appa, kuser)
	appa = nxtAddVerToDoc(appa, appAttrHdr)
	return nxtConvertToJSONBytes(appa), true
}

// Remove app attributes for a apps on disconnect
func nxtPurgeAppAttrJSON(appid string) {
	appAttrLock.Lock()
	defer appAttrLock.Unlock()
	delete(appAttr, appid)
}

// Update cache because version info changed for collection
func nxtUpdateAppAttrCache() {
	var appC usrCache

	appAttrLock.Lock()

	for id, _ := range appAttr {
		appa, _ := nxtReadAppAttrDB(id)
		appC.uajson = appa
		appAttr[id] = appC
	}

	appAttrLock.Unlock()
	nxtLogDebug("AppAttrCache", fmt.Sprintf("Updated %v entries in local cache", len(appAttr)))
}

//--------------------------Attributes Collection functions--------------------------

// Read all records (documents) from collection in DB
// Add header document fields (versions, tenant, ...) to each attribute doc
// Convert to json and return a consolidated attributes file (collection)
func nxtCreateRefDataDoc(ctx context.Context, ucase string, keyid string, istr string) []byte {

	var attrstr string
	var docs []bson.M
	var changeby, changeat, vers string

	qsm := QStateMap[ucase]
	tsm := TStateMap[ucase]
	coll := qsm.DColl
	cursor, err := CollMap[coll].Find(ctx, bson.M{})
	if err != nil {
		nxtLogError(ucase, fmt.Sprintf("Failed to find any attribute docs - %v", err))
		nxtMongoError()
		return []byte("")
	}
	if err = cursor.All(ctx, &docs); err != nil {
		nxtLogError(ucase, fmt.Sprintf("Read failure for attributes - %v", err))
		return []byte("")
	}

	attrstr = istr
	ndocs := len(docs)
	tsm.Count = 0
	addComma := false
	for i := 0; i < ndocs; i++ {

		if docs[i]["_id"].(string) == HDRKEY { // Version doc
			tsm.Keys[i] = HDRKEY
			tsm.Count = tsm.Count + 1
			vers = fmt.Sprintf("%v", docs[i]["minver"])
			changeby = docs[i]["changeby"].(string)
			changeat = docs[i]["changeat"].(string)
			continue
		}

		// Convert map structure to json
		// Concatenate json strings for attributes of each app bundle
		tsm.Keys[i] = docs[i]["_id"].(string)
		tsm.Count = tsm.Count + 1
		docs[i] = nxtFixupAttrID(docs[i], keyid)
		docs[i] = nxtAddVerToDoc(docs[i], qsm.RefHdr)
		if addComma {
			attrstr = attrstr + ",\n"
		}
		attrstr = attrstr + nxtConvertToJSONString(docs[i])
		addComma = true
	}
	attrstr = attrstr + "\n]\n}"
	msg := "Loaded version " + vers + " of "
	msg += coll + " changed by " + changeby + " at " + changeat
	nxtLogDebug(ucase, msg)
	return []byte(attrstr)
}

//--------------------------------Authz functions-----------------------------------------
//
// App Bundle Access Authorization
// Evaluate the app access query using a user's attributes and a target app bundle ID.
// Return true or false
// Minion code receives a packet to be sent to a Connector
// It takes the user attributes and target app bundle ID for destination Connector to
// call this API for app access authz
// It gets back a true or false as the authz result.
func nxtEvalAppAccessAuthz(ucase string, uattr string, bid string) bool {
	// Combine to create {"bid": <bundle id>, "user": { <user attribute key-value pairs> }}

	if ucase != QStateMap[ucase].QUCase {
		return false
	}
	if QStateMap[ucase].QError {
		return false
	}
	rs, ok := nxtExecOpaQry(nxtEvalAccessInputJSON(bid, uattr), ucase)
	if ok {
		retval := fmt.Sprintf("%v", rs[0].Expressions[0].Value)
		return retval == "true"
	}
	nxtLogError(ucase, fmt.Sprintf("Query execution failure for access to %s", bid))
	return false
}

func nxtEvalAccessInputJSON(bid string, uajson string) []byte {
	str1 := "{\"" + kbid + "\": \""
	str2 := "\", \"" + kuserattrs + "\": "
	jsonResp := str1 + bid + str2 + uajson + " }"
	return []byte(jsonResp)
}

//
//--------------------------------- User Routing ----------------------------------
//
// User Route Policy
// Evaluate the user routing query using a user's attributes and a destination host.
// When the minion code in an apod receives a packet to be forwarded, it calls the
// API for routing policy with the user id, and destination host.
// API returns a string tag which may be null for default case or "deny" to block host
// access. Minion uses the tag to determine the routing or blocking host access.
func nxtEvalUserRouting(which string, ucase string, uid string, uajson string, host string) string {
	// Combine to create {"host": <host id>, "user": { <user attribute key-value pairs> }}
	if ucase != QStateMap[ucase].QUCase {
		return ""
	}
	if QStateMap[ucase].QError {
		nxtLogError(ucase, "Qstate error for route query for "+uid+" to "+host)
		return ""
	}
	rs, ok := nxtExecOpaQry(nxtEvalRoutingInputJSON(host, uajson), ucase)
	if ok {
		return (fmt.Sprintf("%v", rs[0].Expressions[0].Value))
	}
	nxtLogError(ucase, "Query execution failure for "+uid+" to "+host)
	return ""
}

func nxtEvalRoutingInputJSON(host string, uajson string) []byte {
	str1 := "{\"" + khost + "\": \""
	str2 := "\", \"" + kuserattrs + "\": "
	jsonResp := str1 + host + str2 + uajson + " }"
	return []byte(jsonResp)
}

//
//--------------------------------- User Tracing ----------------------------------
//
// User Tracing Policy
// Evaluate the tracing query using a user's attributes and trace request attributes.
// When the minion code in an apod receives a stream to be forwarded, it calls the
// API for tracing policy with the user attributes.
// API returns a string which can be either "no" or "none" if the flow is not to be
// traced, else a configured string that represents the trace request id. This trace
// request id is then inserted in the trace spans so that the spans can be matched
// back to the trace request. For eg., for a request to trace all nonemployee users
// who are located in California, the request id could be "NonemployeeCaliforniaUsers".
// The spans for traced flows will then contain the tag
//   "nxt-trace-requestid": "NonemployeeCaliforniaUsers"
func nxtEvalUserTracing(ucase string, uattr string) string {
	// Create {"user": { <user attribute key-value pairs> }}

	if ucase != QStateMap[ucase].QUCase {
		return "{\"no\": []}"
	}
	if QStateMap[ucase].QError {
		nxtLogError(ucase, "Qstate error for trace query")
		return "{\"no\": []}"
	}
	rs, ok := nxtExecOpaQry(nxtEvalTracingInputJSON(uattr), ucase)
	if ok {
		jsonResp, merr := json.Marshal(rs[0].Expressions[0].Value)
		if merr != nil {
			nxtLogError("JSON-marshal", fmt.Sprintf("%v for %v", merr,
				rs[0].Expressions[0].Value))
			return "{\"no\": []}"
		}
		return string(jsonResp)
	}
	nxtLogError(ucase, "Trace query execution failure")
	return "{\"no\": []}"
}

func nxtEvalTracingInputJSON(uajson string) []byte {
	str1 := "{\"" + kuserattrs + "\": "
	jsonResp := str1 + uajson + " }"
	return []byte(jsonResp)
}

//
//--------------------------------- Stats Policy ----------------------------------
//
// Stats Policy
// Evaluate the policy to return a set of user attributes to be used for stats.
// Minion code in an apod evaluates this policy once at the beginning (or periodically
// in case policy changes ?)
func nxtEvalUserStats(ucase string) string {
	// Create {"user": {"attributes": "stats"}}

	if ucase != QStateMap[ucase].QUCase {
		return ""
	}
	if QStateMap[ucase].QError {
		nxtLogError(ucase, "Qstate error for stats query")
		return ""
	}
	rs, ok := nxtExecOpaQry(nxtEvalStatsInputJSON("{\"attributes\": \"stats\"}"), ucase)
	if ok {
		jsonResp, merr := json.Marshal(rs[0].Expressions[0].Value)
		if merr != nil {
			nxtLogError("JSON-marshal", fmt.Sprintf("%v for %v", merr,
				rs[0].Expressions[0].Value))
			return ""
		}
		return string(jsonResp)
	}
	nxtLogError(ucase, "Stats query execution failure")
	return ""
}

func nxtEvalStatsInputJSON(uajson string) []byte {
	str1 := "{\"" + kuserattrs + "\": "
	jsonResp := str1 + uajson + " }"
	return []byte(jsonResp)
}

//---------------------------------Rego interface functions-----------------------------
// Prime the load directory with the policy file and the reference data file
func nxtPrimeLoadDir(ucase string) {

	dirname := QStateMap[ucase].LDir
	if QStateMap[ucase].NewPol {
		QStateMap[ucase].NewPol = false
		err := ioutil.WriteFile(dirname+"/policyfile.rego", QStateMap[ucase].RegoPol, 0644)
		if err != nil {
			nxtLogError(ucase, fmt.Sprintf("Policy loading in dir %s failed - %v", dirname, err))
			// TODO: Can we avoid this ?
			log.Fatal(err)
		}
	}

	if QStateMap[ucase].NewData {
		QStateMap[ucase].NewData = false
		// Write reference data to load directory
		err := ioutil.WriteFile(dirname+"/refdata.json", QStateMap[ucase].RefData, 0644)
		if err != nil {
			nxtLogError(ucase, fmt.Sprintf("Refdata loading in dir %s failed - %v", dirname, err))
			log.Fatal(err)
		}
	}
	// Free up memory held in RefData and RegoPol once written to disk.
	var nullb []byte
	QStateMap[ucase].RegoPol = nullb
	QStateMap[ucase].RefData = nullb
}

// Create rego object for the query
func nxtCreateOpaQry(query string, ldir string) *rego.Rego {

	r := rego.New(
		rego.Query(query),
		rego.Load([]string{ldir}, nil))
	nxtLogDebug(ldir, "Created OPA query with load directory "+ldir)
	return r
}

// Create a prepared query that can be evaluated.
func nxtPrepOpaQry(ctx context.Context, ucase string, r *rego.Rego) (rego.PreparedEvalQuery, bool) {

	rs, err := r.PrepareForEval(ctx)
	if err != nil {
		nxtLogError(ucase, fmt.Sprintf("OPA query prep failure with error - %v", err))
		return rs, true
	}
	return rs, false
}

// Execute prepared query
func nxtExecOpaQry(inp []byte, ucase string) (rego.ResultSet, bool) {
	// Rego object is pre-created and query prepared for evaluation.
	// Here we only evaluate the prepared query with the input data

	var input interface{}

	ctx := context.Background()

	if err := json.Unmarshal(inp, &input); err != nil {
		nxtLogError(ucase, fmt.Sprintf("Eval input JSON unmarshal error - %v", err))
		return rego.ResultSet{}, false
	}

	// for each prepared query, execute the evaluation.
	rs, err := QStateMap[ucase].PrepQry.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		nxtLogError(ucase, fmt.Sprintf("Evaluation error - %v", err))
		return rego.ResultSet{}, false
	}
	if rs == nil {
		nxtLogError(ucase, fmt.Sprintf("Evaluation with Nil result - %v", err))
		return rego.ResultSet{}, false
	}
	return rs, true
}

//---------------------------------Utility functions----------------------------------
// We need this since the versions and tenant are in a separate header document
func nxtAddVerToDoc(doc bson.M, hdr DataHdr) bson.M {
	doc[kmajver] = hdr.Majver
	doc[kminver] = hdr.Minver
	return doc
}

func nxtFixupAttrID(attr bson.M, keyid string) bson.M {
	attr[keyid] = attr["_id"]
	delete(attr, "_id")
	return attr
}

func nxtConvertToJSONBytes(inp bson.M) []byte {
	jsonResp, merr := json.Marshal(inp)
	if merr != nil {
		nxtLogError("JSON-marshal", fmt.Sprintf("%v for %v", merr, inp))
		return []byte("")
	}
	return jsonResp
}

func nxtConvertToJSONString(inp bson.M) string {
	jsonResp, merr := json.Marshal(inp)
	if merr != nil {
		nxtLogError("JSON-marshal", fmt.Sprintf("%v for %v", merr, inp))
		return ""
	}
	return string(jsonResp)
}

func nxtLogError(ref string, msg string) {
	slog.Error(ref + " - " + msg + ", " + st + ", " + sp)
}

func nxtLogDebug(ref string, msg string) {
	slog.Debug(ref + " - " + msg + ", " + st + ", " + sp)
}

func nxtWriteAttrVersions() {
	qsm2 := QStateMap[opaUseCases[2]] // Access policy usecase
	qsm3 := QStateMap[opaUseCases[3]] // Route policy usecase
	versions := fmt.Sprintf("USER=%d.%d\nBUNDLE=%d.%d\nPOLICY=%d.%d\nROUTE=%d.%d",
		usrAttrHdr.Majver, usrAttrHdr.Minver, qsm2.RefHdr.Majver, qsm2.RefHdr.Minver,
		qsm2.PStruct.Majver, qsm2.PStruct.Minver, qsm3.RefHdr.Majver, qsm3.RefHdr.Minver)
	ioutil.WriteFile("/tmp/opa_attr_versions", []byte(versions), 0644)
	usrAttrWrVer = false
	appAttrWrVer = false
	QStateMap[opaUseCases[2]].WrVer = false
	QStateMap[opaUseCases[3]].WrVer = false
}
