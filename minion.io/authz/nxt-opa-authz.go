package authz

/*************************************
// TODO: break up file into modules
// Nextensio interface for Opa Rego library to provide policy based services covering
// application access authorization, Agent authorization, Connector authorization,
// routing, etc.
// This code will be compiled together with the minion code running in a service pod.
// The minion code will first call nxtAaaInit() to set things up. After that, it will
// call an API specific to the authorization (or whatever) policy check required.
// Some APIs are to be called in ingress service pod, some in egress service pod.
// Common for every pod:
// NxtAAAInit() - to be called once for initialization before any other API calls
// Ingress pod APIs:
//     func NxtGetUsrAttr(userid string) (string, bool)
//     func NxtUsrLeave(userid string)
//     func NxtUsrAllowed(userid string) bool
//     func NxtRouteLookup(userid string, host string, ...)
// Egress pod APIs:
//     func NxtAccessOk(bundleid string, userattr string) bool
//
*************************************/

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/open-policy-agent/opa/rego"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

//
//--------------------------------Data Structures, Variables, etc ----------------------------------

const maxOpaUseCases = 5 // we currently have 4
const maxMongoColls = 10 // assume max 10 MongoDB tenant collections, currently 6+1
const maxUsers = 10000   // max per tenant

/*****************************
// MongoDB database and collections
*****************************/
const nxtMongoDB = "NxtDB" // default DB name prior to per-tenant DBs
const userInfoCollection = "NxtUsers"
const connInfoCollection = "NxtApps"
const appAttrCollection = "NxtAppAttr"
const userAttrCollection = "NxtUserAttr"
const policyCollection = "NxtPolicies"
const hostAttrCollection = "NxtHostAttr"
const RouteCollection = "NxtRoutes"

const AAuthzQry = "data.app.access.allow"
const CAuthzQry = "data.app.access.allow"
const AccessQry = "data.app.access.allow"
const RouteQry = "data.user.routing.route_tag"

// These need to be created via the Dockerfiles for the policy and refdata files
// required at run-time by OPA
const agentldir = "authz/agent-authz"
const connldir = "authz/conn-authz"
const accessldir = "authz/app-access"
const routeldir = "authz/routing"

const agentauthzpolicy = "agent-authz.rego"
const connauthzpolicy = "conn-authz.rego"
const appaccesspolicy = "app-access.rego"
const userroutepolicy = "user-routing.rego"

const userExtAttrDocKey = "UserExtAttr"
const HDRKEY = "Header"

// These are key names that should be used in the OPA Rego policies
const kmajver = "maj_ver" // Collection major version
const kminver = "min_ver" // Collection minor version
const kbid = "bid"        // App-bundle ID from AppInfo and AppAttr collections
const kuser = "uid"       // User ID from UserInfo and UserAttr collections
const khost = "host"      // Host ID from HostAttr collection
// These are key names that anchor a group of attributes for use in OPA Rego policies
const kbundleattrs = "bundles" // Anchor for all refdata app-bundle attributes
const khostattrs = "hosts"     // Anchor for all refdata host attributes
const kuserattrs = "user"      // Anchor for input user attributes in routing
//const khostrouteattrs = "routeattrs" // Anchor for attributes per route tag for a host

// Common struct for all policies
// Can't seem to add a Rego policy via mongoshell, hence the hack to also
// provide a filename that is then used to read the policy from a local file.
type Policy struct {
	PolicyId string `json:"pid" bson:"_id"`
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
	ID     string `bson:"_id" json:"ID"`
	Majver int    `bson:"majver" json:"majver"`
	Minver int    `bson:"minver" json:"minver"`
}

// Data object to track every use case
type QState struct {
	NewVer   bool                   // new version of policy or refdata
	NewPol   bool                   // new version of rego poloicy
	NewData  bool                   // new version of reference data
	WrVer    bool                   // write versions to file (for testing infra)
	QCreated bool                   // query object created
	QError   bool                   // error in query state
	Qry      string                 // the OPA Rego query
	QUCase   string                 // query use case
	QryObj   *rego.Rego             // raw query
	PrepQry  rego.PreparedEvalQuery // compiled query
	PolType  string                 // key for policy
	PStruct  Policy                 // Policy struct
	RegoPol  []byte                 // rego policy
	LDir     string                 // load directory for OPA
	DColl    string                 // name of reference data collection
	HdrKey   string                 // Header doc keys for reference data collections
	RefHdr   DataHdr                // reference data header doc
	RefData  []byte                 // reference data
}

// Info for test cases
type TState struct { // For testing
	Count int         // Count of keys
	Keys  [500]string // Keys - Bids or Hosts or ...
}

var QStateMap = make(map[string]*QState, maxOpaUseCases) // indexed by opaUseCases
var TStateMap = make(map[string]*TState, maxOpaUseCases)

var opaUseCases = []string{"AgentAuthz", "ConnAuthz", "AppAccess", "RoutePol"}
var initUseCase = []int{0, 0, 1, 1}
var policyType = []string{"AgentPolicy", "ConnPolicy", "AccessPolicy", "RoutePolicy"} // Policy doc Keys
var hdrKeyNm = []string{"UserInfo", "AppInfo", "AppAttr", "HostAttr"}                 // Ref Data Header doc keys
var hdrKeyNm2 = []string{"UserAttr", "AppAttr", "UserAttr", "UserAttr"}               // Header doc keys for associated cltns
var opaQuery = []string{AAuthzQry, CAuthzQry, AccessQry, RouteQry}
var loadDir = []string{agentldir, connldir, accessldir, routeldir}
var DColls = []string{userInfoCollection, connInfoCollection, appAttrCollection, hostAttrCollection}

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
var nxtDisconnect func(string, *zap.SugaredLogger)
var slog *zap.SugaredLogger
var st, sg, sm zap.Field // for tenant, gateway, module

var CollMap map[string]*mongo.Collection
var mongoClient *mongo.Client
var nxtMongoDBName string // set to nxtMongoDB (legacy) or a unique name per tenant going forward

/******************************** Calls from minion ******************/
func NxtAaaInit(namespace string, pod string, gateway string, mongouri string, sl *zap.SugaredLogger, disconnectCb func(string, *zap.SugaredLogger)) int {
	err := nxtOpaInit(namespace, pod, gateway, mongouri, sl, disconnectCb)
	if err != nil {
		return 1
	}
	return 0
}

// Check on Apod for user and Cpod for connector
func NxtUsrAllowed(which string, userid string, cluster string, podname string) bool {
	if initDone == false || mongoInitDone == false {
		return false
	}
	if (cluster == "gateway") || (cluster == "any") {
		return true
	}
	gw := cluster + ".nextensio.net"
	if gw != nxtGw {
		nxtLogDebug("UsrAllow", fmt.Sprintf("Gw mismatch for %s, gw=%s", userid, gw))
		return false
	}
	if podname != nxtPod {
		nxtLogDebug("UsrAllow", fmt.Sprintf("Pod mismatch for %s, pod=%s", userid, podname))
		return false
	}
	return true
}

// Purge a user's attributes when last agent of that user leaves
func NxtUsrLeave(which string, userid string) {
	if initDone == false {
		return
	}
	if which == "agent" {
		// Don't check mongoInitDone as we purge the data in local cache
		nxtPurgeUserAttrJSON(userid)
	}
}

// Get user attributes only on Apod. Passed to Cpod via flow header.
func NxtGetUsrAttr(which string, userid string) (string, bool) {
	if initDone == false {
		return "", false
	}
	// Don't check mongoInitDone as we may have the data in local cache
	// The call handles the case where mongo init not done and data not in local cache
	if which == "agent" {
		uajson, _, ok := nxtGetUserAttrJSON(userid)
		return uajson, ok
	} else {
		return "", false
	}
}

// Access policy is run only on Cpod
func NxtAccessOk(which string, bundleid string, userattr string) bool {
	if initDone == false {
		return false
	}
	if which == "agent" {
		return true
	}
	return nxtEvalAppAccessAuthz(opaUseCases[2], userattr, bundleid)
}

// Route policy is run only on Apod
func NxtRouteLookup(which string, uid string, host string) string {
	if initDone == false || mongoInitDone == false {
		return ""
	}
	if which != "agent" {
		return ""
	}
	return nxtEvalUserRouting(opaUseCases[3], uid, host, nil)
}

const RouteTag = "tag"

/*********************************************************************/

func authzMain() {
}

// For now, this function tests access for a number of users with each app bundle.
// In production, this will monitor for DB updates and pull in any modified documents
// to reinitialize any OPA stuff
func nxtOpaProcess(ctx context.Context) int {

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
	nxtLogDebug("OpaProcess", fmt.Sprintf("2. initDone=%v, mongoInitDone=%v, mongoCheck=%v",
		initDone, mongoInitDone, mongoCheck))
	for {
		// sleep(1 sec)
		time.Sleep(1 * 1000 * time.Millisecond)

		nxtOpaProcessMongoCheck(ctx)

		if mongoInitDone == false {
			// Skip any further code if mongoDB not accessible
			continue
		}

		for i, ucase := range opaUseCases {
			if initUseCase[i] > 0 {
				nxtSetupUseCase(ctx, i, ucase)
			}
		}
		// Process if new version of UserAttr collection
		nxtProcessUserAttrChanges(ctx)

		if (QStateMap[opaUseCases[0]].WrVer == true) ||
			(QStateMap[opaUseCases[2]].WrVer == true) ||
			(QStateMap[opaUseCases[3]].WrVer == true) {
			nxtWriteAttrVersions()
		}

	}
	return 0
}

func nxtOpaProcessInitCheck(ctx context.Context) {
	var err error
	nxtLogDebug("OpaProcess", fmt.Sprintf("1. initDone=%v, mongoInitDone=%v, mongoCheck=%v",
		initDone, mongoInitDone, mongoCheck))
	for {
		if initDone == true {
			break
		}
		time.Sleep(2 * 1000 * time.Millisecond)
		if mongoInitDone == false {
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
	if mongoInitDone == false {
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
	if mongoCheck == true {
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

//-------------------------------- Init Functions ----------------------------------
// API to init nxt OPA interface
func nxtOpaInit(ns string, pod string, gateway string, mongouri string, sl *zap.SugaredLogger, disconnectCb func(string, *zap.SugaredLogger)) error {
	defer func() {
		initExit <- true
	}()

	var err error

	if procStarted {
		return nil
	}

	ctx := context.Background()
	slog = sl
	tenant = ns
	nxtPod = pod
	nxtGw = gateway
	st = zap.String("Tenant", tenant)
	sg = zap.String("GW", nxtGw)
	sm = zap.String("Module", "NxtOPA")

	nxtMongoDBName = nxtGetTenantDBName(tenant)
	mongoDBAddr = mongouri
	nxtDisconnect = disconnectCb

	go nxtOpaProcess(ctx)
	procStarted = true

	mongoClient, err = nxtMongoDBInit(ctx, ns, mongouri)
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
	nxtReadUserExtAttrDoc(ctx)

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
	db := cl.Database(nxtMongoDBName)
	nxtLogDebug(nxtMongoDBName, fmt.Sprintf("The DB being used for tenant %s", ns))

	// Required on both apod and cpod
	CollMap[policyCollection] = db.Collection(policyCollection)
	// Required on cpod only
	CollMap[connInfoCollection] = db.Collection(connInfoCollection)
	CollMap[appAttrCollection] = db.Collection(appAttrCollection)
	// Required on apod only
	CollMap[userInfoCollection] = db.Collection(userInfoCollection)
	CollMap[hostAttrCollection] = db.Collection(hostAttrCollection)
	// Required on apod. Required on cpod for testing app-access authz
	CollMap[userAttrCollection] = db.Collection(userAttrCollection)

	CollMap[RouteCollection] = db.Collection(RouteCollection) // temporary

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

func nxtGetEnvStr(key string, defaultValue string) string {
	v := os.Getenv(key)
	if v == "" {
		v = defaultValue
	}
	return v
}

func nxtGetEnvInt(key string, defaultValue int) int {
	v := os.Getenv(key)
	if v != "" {
		if val, err := strconv.Atoi(v); err == nil {
			return val
		}
	}
	return defaultValue
}

// Create use case for each query type
func nxtCreateOpaUseCase(i int, ucase string) {
	var NewState QState
	var NewTS TState

	hdrKeyNm[i] = nxtGetHdrKey(hdrKeyNm[i])
	hdrKeyNm2[i] = nxtGetHdrKey(hdrKeyNm2[i])

	NewState.QUCase = ucase
	NewState.PolType = policyType[i]
	NewState.LDir = loadDir[i]
	NewState.Qry = opaQuery[i]
	NewState.HdrKey = hdrKeyNm[i]
	NewState.DColl = DColls[i]
	NewState.QError = true
	QStateMap[ucase] = &NewState
	TStateMap[ucase] = &NewTS
	nxtLogDebug(ucase, fmt.Sprintf("Use case created for policy %s, refdata %s", policyType[i], DColls[i]))
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
	if qs.NewVer == true {
		// A new policy and/or new data collection is available
		// If their major versions match, set up the Data load directory
		if qs.PStruct.Majver == qs.RefHdr.Majver {
			nxtPrimeLoadDir(ucase)
			qs.NewVer = false
			if qs.QCreated == false {
				qs.QryObj = nxtCreateOpaQry(qs.Qry, qs.LDir)
				qs.QCreated = true
			}
			qs.PrepQry, qs.QError = nxtPrepOpaQry(ctx, qs.QryObj, ucase)
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
	err := CollMap[coll].FindOne(ctx, bson.M{"_id": qs.HdrKey}).Decode(&hdr)
	if err != nil {
		nxtLogError(ucase, fmt.Sprintf("Failed to find %s header doc - %v", qs.HdrKey, err))
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
		QStateMap[ucase].RefData = nxtCreateCollJSON(ctx, ucase, kbid, "{ \""+kbundleattrs+"\":  [")
		return
	case opaUseCases[3]:
		QStateMap[ucase].RefData = nxtCreateCollJSON(ctx, ucase, khost, "{ \""+khostattrs+"\":  [")
		return
	}
}

//-----------------------------User Attributes Caching Functions------------------------------

// Cache of user attributes for all active users. Cache is updated whenever
// the collection version changes. Cache entries are purged when a user disconnects.
type usrCache struct {
	uajson string
	uabson primitive.M
}

var userAttr map[string]usrCache
var userAttrLock sync.Mutex
var usrAttrHdr DataHdr

// If new version of user attribues collection is available, read the
// extended attributes spec doc and update the cache for active users
func nxtProcessUserAttrChanges(ctx context.Context) {
	tmphdr := nxtReadUserAttrHdr(ctx)
	if (tmphdr.Majver > usrAttrHdr.Majver) || (tmphdr.Minver > usrAttrHdr.Minver) {
		usrAttrHdr = tmphdr
		QStateMap[opaUseCases[0]].WrVer = true // for testing infra
		nxtReadUserExtAttrDoc(ctx)
		nxtUpdateUserAttrCache()
	}
}

// Read header doc from user attributes collection to get version info
func nxtReadUserAttrHdr(ctx context.Context) DataHdr {
	// read header document for user attr collection used as input
	var uahdr DataHdr
	var errhdr = DataHdr{ID: hdrKeyNm2[0], Majver: 0, Minver: 0}

	coll := CollMap[userAttrCollection]
	err := coll.FindOne(ctx, bson.M{"_id": hdrKeyNm2[0]}).Decode(&uahdr)
	if err != nil {
		nxtLogError(hdrKeyNm2[0], fmt.Sprintf("Failed to find user attr header doc - %v", err))
		nxtMongoError()
		return errhdr
	}
	return uahdr
}

func loadUserAttrFromDB(uuid string) (string, primitive.M, bool) {
	var uaC usrCache

	uastruct, ok := nxtReadUserAttrDB(uuid)
	if ok {
		ua := nxtConvertToJSON(uastruct)
		userAttrLock.Lock()
		// Cache doc read from DB
		uaC.uajson = ua
		uaC.uabson = uastruct
		userAttr[uuid] = uaC
		userAttrLock.Unlock()
		return ua, uastruct, ok
	}
	return "", primitive.M{}, false
}

// Read one user's attr data from mongoDB collection and return it together
// with the json version with header info added. Called when user connects to service pod.
func nxtGetUserAttrJSON(uuid string) (string, primitive.M, bool) {
	uajson, uastruct, ok := nxtReadUserAttrCache(uuid)
	if ok {
		return uajson, uastruct, ok // cached version
	}

	return loadUserAttrFromDB(uuid)
}

// Read a user attribute doc from local cache
func nxtReadUserAttrCache(uuid string) (string, primitive.M, bool) {
	userAttrLock.Lock()
	defer userAttrLock.Unlock()

	// Check in cache if user's attributes exist. If yes, return value.
	uaC, ok := userAttr[uuid]
	if ok == true {
		//nxtLogDebug(uuid, "Retrieved attributes for user from local cache")
		return uaC.uajson, uaC.uabson, true
	}
	//nxtLogDebug(uuid, "Failed to find attributes for user in local cache")
	return "", nil, false
}

// Read a user attribute doc from the DB and add header document info
func nxtReadUserAttrDB(uuid string) (bson.M, bool) {
	var usera bson.M

	if mongoInitDone == false {
		return bson.M{}, false
	}

	ctx := context.Background()

	// Read user attributes from DB, cache json version, and return it
	coll := CollMap[userAttrCollection]
	err := coll.FindOne(ctx, bson.M{"_id": uuid}).Decode(&usera)
	if err != nil {
		nxtLogError(uuid, fmt.Sprintf("Failed to find attributes doc for user - %v", err))
		nxtMongoError()
		return bson.M{}, false
	}
	usera = nxtFixupAttrID(usera, kuser)
	usera = nxtAddVerToDoc(usera, usrAttrHdr)
	return usera, true
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
		uastruct, _ := nxtReadUserAttrDB(id)
		uaC.uajson = nxtConvertToJSON(uastruct)
		uaC.uabson = uastruct
		userAttr[id] = uaC
	}

	userAttrLock.Unlock()
	nxtLogDebug("UserAttrCache", fmt.Sprintf("Updated %v entries in local cache", len(userAttr)))
}

//-----------------------------Bundle Attributes Caching Functions-----------------------------

var appAttr map[string]usrCache
var appAttrLock sync.Mutex
var appAttrHdr DataHdr

// If new version of app attribues collection is available, read the
// attributes docs and update the cache for active apps
func nxtProcessAppAttrChanges(ctx context.Context) {
	tmphdr := nxtReadAppAttrHdr(ctx)
	if (tmphdr.Majver > appAttrHdr.Majver) || (tmphdr.Minver > appAttrHdr.Minver) {
		appAttrHdr = tmphdr
		QStateMap[opaUseCases[2]].WrVer = true // for testing infra
		nxtUpdateAppAttrCache()
	}
}

// Read header doc from app attributes collection to get version info
func nxtReadAppAttrHdr(ctx context.Context) DataHdr {
	// read header document for app attr collection used as input
	var apphdr DataHdr
	var errhdr = DataHdr{ID: HDRKEY, Majver: 0, Minver: 0}

	coll := CollMap[appAttrCollection]
	err := coll.FindOne(ctx, bson.M{"_id": HDRKEY}).Decode(&apphdr)
	if err != nil {
		nxtLogError(hdrKeyNm2[0], fmt.Sprintf("Failed to find app attr header doc - %v", err))
		nxtMongoError()
		return errhdr
	}
	return apphdr
}

func loadAppAttrFromDB(uuid string) (string, primitive.M, bool) {
	var appC usrCache

	appstruct, ok := nxtReadAppAttrDB(uuid)
	if ok {
		app := nxtConvertToJSON(appstruct)
		appAttrLock.Lock()
		// Cache doc read from DB
		appC.uajson = app
		appC.uabson = appstruct
		appAttr[uuid] = appC
		appAttrLock.Unlock()
		return app, appstruct, ok
	}
	return "", primitive.M{}, false
}

// Read one app's attr data from mongoDB collection and return it together
// with the json version with header info added. Called when app connects to service pod.
func nxtGetAppAttrJSON(uuid string) (string, primitive.M, bool) {
	app, appstruct, ok := nxtReadAppAttrCache(uuid)
	if ok {
		return app, appstruct, ok // cached version
	}

	return loadAppAttrFromDB(uuid)
}

// Read an app attribute doc from local cache
func nxtReadAppAttrCache(uuid string) (string, primitive.M, bool) {
	appAttrLock.Lock()
	defer appAttrLock.Unlock()

	appC, ok := appAttr[uuid]
	if ok == true {
		return appC.uajson, appC.uabson, true
	}
	return "", nil, false
}

// Read an app attribute doc from the DB and add header document info
func nxtReadAppAttrDB(appid string) (bson.M, bool) {
	var appa bson.M

	if mongoInitDone == false {
		return bson.M{}, false
	}

	ctx := context.Background()

	// Read app attributes from DB, cache json version, and return it
	coll := CollMap[appAttrCollection]
	err := coll.FindOne(ctx, bson.M{"_id": appid}).Decode(&appa)
	if err != nil {
		nxtLogError(appid, fmt.Sprintf("Failed to find attributes doc for app - %v", err))
		nxtMongoError()
		return bson.M{}, false
	}
	appa = nxtFixupAttrID(appa, kuser)
	appa = nxtAddVerToDoc(appa, appAttrHdr)
	return appa, true
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
		appstruct, _ := nxtReadAppAttrDB(id)
		appC.uajson = nxtConvertToJSON(appstruct)
		appC.uabson = appstruct
		appAttr[id] = appC
	}

	appAttrLock.Unlock()
	nxtLogDebug("AppAttrCache", fmt.Sprintf("Updated %v entries in local cache", len(appAttr)))
}

//-------------------------------Dynamic User Attributes Functions---------------------------------

// Extended (runtime) user attributes spec. The mongoDB doc specifies the HTTP headers
// as a JSON string of key-value pairs in Attrlist. The key is seen by OPA. The
// value is the HTTP header name used to retrieve the attribute value from the
// user packet.
// For eg., in extUAttr:
//  ["devOS"] -> "x-nxt-devOS"
//  ["osver"] -> "x-nxt-osver"
//  ["loc"]   -> "x-nxt-location"
//
// In extUAValues:
//  ["devOS"] -> "<devOS string value>"
//  ["osver"] -> <osver float64 value>
//  ["loc"]   -> "<location string value>"
type UserExtAttr struct {
	Uid      string `bson:"_id" json:"uid"`
	Attrlist string `bson:"attrlist" json:"attrlist"`
}

var extUAttr = make(map[string]interface{}, maxExtUAttr)
var extUAValues = make(map[string]interface{}, maxExtUAttr)

const maxExtUAttr = 10 // assume max 10 such attributes

// Read spec document for extended attributes from mongoDB during init
// and whenever collection version changes
func nxtReadUserExtAttrDoc(ctx context.Context) {
	var uahdr UserExtAttr

	coll := CollMap[userAttrCollection]
	err := coll.FindOne(ctx, bson.M{"_id": userExtAttrDocKey}).Decode(&uahdr)
	if err != nil {
		// Disable this error until it's fully implemented
		//nxtLogError(userExtAttrDocKey, fmt.Sprintf("Failed to read user extended attributes doc - %v", err))
		//nxtMongoError()
		return
	}

	// Cache spec read from DB
	for k := range extUAttr {
		delete(extUAttr, k)
	}
	if err := json.Unmarshal([]byte(uahdr.Attrlist), &extUAttr); err != nil {
		nxtLogError(userExtAttrDocKey, fmt.Sprintf("Unmarshal error for user extended attributes - %v", err))
	}
}

// Get the attributes from HTTP headers for every call from minion
func nxtGetUserAttrFromHTTP(uid string, hdr *http.Header) string {
	for k := range extUAValues {
		delete(extUAValues, k)
	}
	for idx, val := range extUAttr {
		hval := hdr.Get(fmt.Sprintf("%s", val))
		fval, err := strconv.ParseFloat(hval, 64)
		if err != nil {
			extUAValues[idx] = hval
		} else {
			extUAValues[idx] = fval
		}
	}
	if len(extUAValues) == 0 {
		return ""
	}
	uajson, err := json.Marshal(&extUAValues)
	if err != nil {
		nxtLogError(uid, fmt.Sprintf("Extended attributes JSON marshal error - %v", err))
		return ""
	}
	return string(uajson)
}

//--------------------------Attributes Collection functions--------------------------

// Read all records (documents) from collection in DB
// Add header document fields (versions, tenant, ...) to each attribute doc
// Convert to json and return a consolidated attributes file (collection)
func nxtCreateCollJSON(ctx context.Context, ucase string, keyid string, istr string) []byte {

	var attrstr string
	var docs []bson.M

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

		if docs[i]["_id"] == qsm.HdrKey { // Version doc
			tsm.Keys[i] = qsm.HdrKey
			tsm.Count = tsm.Count + 1
			continue
		}

		// Convert map structure to json
		// Concatenate json strings for attributes of each app bundle
		tsm.Keys[i] = fmt.Sprintf("%s", docs[i]["_id"])
		tsm.Count = tsm.Count + 1
		docs[i] = nxtFixupAttrID(docs[i], keyid)
		docs[i] = nxtAddVerToDoc(docs[i], qsm.RefHdr)
		if addComma == true {
			attrstr = attrstr + ",\n"
		}
		attrstr = attrstr + nxtConvertToJSON(docs[i])
		addComma = true
	}
	attrstr = attrstr + "\n]\n}"
	return []byte(attrstr)
}

//--------------------------------Authz functions-----------------------------------------
//
// App Bundle Access Authorization
// Evaluate the app access query using a user's attributes and a target app bundle ID.
// Return true or false
// Minion code receives a packet to be sent to a Connector
// It takes the HTTP header for user attributes and target app bundle ID for destination
// Connector to call the API for app access authz
// It gets back a true or false as the authz result.
func nxtEvalAppAccessAuthz(ucase string, uattr string, bid string) bool {
	// Unmarshal uattr into a UserAttr struct and insert bid into it
	// Convert back to a unified json string
	// Call nxtEvalAppAccessAuthzCore() with json string
	var ua bson.M

	if ucase != QStateMap[ucase].QUCase {
		return false
	}
	if QStateMap[ucase].QError {
		return false
	}
	if err := json.Unmarshal([]byte(uattr), &ua); err != nil {
		nxtLogError(ucase, fmt.Sprintf("Eval input JSON unmarshal error - %v", err))
		return false
	}
	ua[kbid] = bid
	rs, ok := nxtExecOpaQry([]byte(nxtConvertToJSON(ua)), ucase)
	if ok {
		retval := fmt.Sprintf("%v", rs[0].Expressions[0].Value)
		return retval == "true"
	}
	nxtLogError(ucase, fmt.Sprintf("Query execution failure for %s access to %s", ua[kuser], bid))
	return false
}

func nxtEvalAgentAuthz(ctx context.Context, ldir string, inp []byte) bool {

	// ldir is a directory containing the policy and the user info record
	// inp is the Input from the "hello" packet received from Agent
	// For Agent authz, create Rego object, prepare query for eval, and evaluate in one stroke

	ucase := opaUseCases[0]
	QS := QStateMap[ucase]
	r := nxtCreateOpaQry(QS.Qry, QS.LDir)

	// Create a prepared query that can be evaluated.
	QS.PrepQry, QS.QError = nxtPrepOpaQry(ctx, r, ucase)

	rs, ok := nxtExecOpaQry(inp, ucase)
	if ok {
		retval := fmt.Sprintf("%v", rs[0].Expressions[0].Value)
		return retval == "true"
	}
	nxtLogError(ucase, "Query execution failure for "+string(inp))
	return false
}

func nxtEvalConnectorAuthz(ctx context.Context, inp []byte) bool {

	ucase := opaUseCases[1]
	rs, ok := nxtExecOpaQry(inp, ucase)
	if ok {
		retval := fmt.Sprintf("%v", rs[0].Expressions[0].Value)
		return retval == "true"
	}
	nxtLogError(ucase, "Query execution failure for "+string(inp))
	return false
}

//
//--------------------------------- User Routing ----------------------------------
//
// User Route Policy
// Evaluate the user routing query using a user's attributes and a destination host.
// When the minion code in an apod receives a packet to be forwarded, it calls the
// API for routing policy with the user id, destination host and the HTTP headers.
// API returns a string tag which may be null for default case. Minion uses the tag
// to determine the routing.
func nxtEvalUserRouting(ucase string, uid string, host string, hdr *http.Header) string {
	// Use uid to get user attributes from local cache, hdr to get runtime attributes
	// from HTTP headers. Combine these attributes with host to generate a unified json
	// string of the form:
	// {"host": "<url>", "dbattr": {<attributes from DB>}, "dynattr": {<attributes from HTTP headers>}}
	// Call nxtEvalUserRoutingCore() with json string

	if ucase != QStateMap[ucase].QUCase {
		return ""
	}
	if QStateMap[ucase].QError {
		nxtLogError(ucase, "Qstate error for route query for "+uid+" to "+host)
		return ""
	}
	uajson, _, _ := nxtGetUserAttrJSON(uid)
	//ueajson := nxtGetUserAttrFromHTTP(uid, hdr)
	rs, ok := nxtExecOpaQry(nxtEvalUserRoutingJSON(host, uajson), ucase)
	if ok {
		return (fmt.Sprintf("%v", rs[0].Expressions[0].Value))
	}
	nxtLogError(ucase, "Query execution failure for "+uid+" to "+host)
	return ""
}

func nxtEvalUserRoutingJSON(host string, uajson string) []byte {
	str1 := "{\"" + khost + "\": \""
	str2 := "\", \"" + kuserattrs + "\": "
	//str3 := ", \"dynattr\": "
	//jsonResp := fmt.Sprintf("%s%s%s%s%s%s }", str1, host, str2, uajson, str3, ueajson)
	jsonResp := fmt.Sprintf("%s%s%s%s }", str1, host, str2, uajson)
	return []byte(jsonResp)
}

//---------------------------------Rego interface functions-----------------------------
// Prime the load directory with the policy file and the reference data file
func nxtPrimeLoadDir(ucase string) {

	dirname := QStateMap[ucase].LDir
	if QStateMap[ucase].NewPol == true {
		QStateMap[ucase].NewPol = false
		err := ioutil.WriteFile(dirname+"/policyfile.rego", QStateMap[ucase].RegoPol, 0644)
		if err != nil {
			nxtLogError(ucase, fmt.Sprintf("Policy loading in dir %s failed - %v", dirname, err))
			// TODO: Can we avoid this ?
			log.Fatal(err)
		}
	}

	if QStateMap[ucase].NewData == true {
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

	var r *rego.Rego
	r = rego.New(
		rego.Query(query),
		rego.Load([]string{ldir}, nil))
	nxtLogDebug(ldir, "Created OPA query with load directory")
	return r
}

// Create a prepared query that can be evaluated.
func nxtPrepOpaQry(ctx context.Context, r *rego.Rego, ucase string) (rego.PreparedEvalQuery, bool) {

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

func nxtConvertToJSON(inp bson.M) string {
	jsonResp, merr := json.Marshal(inp)
	if merr != nil {
		nxtLogError("JSON-marshal", fmt.Sprintf("%v for %v", merr, inp))
		return ""
	}
	return string(jsonResp)
}

func nxtGetHdrKey(val string) string {
	return HDRKEY // common name for all header docs
}

func nxtLogError(ref string, msg string) {
	slog.Error(" ", st, sg, sm, zap.String("Ref", ref), zap.String("Msg", msg))
}

func nxtLogInfo(ref string, msg string) {
	slog.Info(" ", st, sg, sm, zap.String("Ref", ref), zap.String("Msg", msg))
}

func nxtLogDebug(ref string, msg string) {
	slog.Debug(" ", st, sg, sm, zap.String("Ref", ref), zap.String("Msg", msg))
}

//-----------------------------------------Test functions-------------------------------
// Test function for application access
// It tests a combination of users with app bundles :
// a) 5 users with max 100 documents populated in mongoDB AppAttr collection
// b) documents in mongoDB UserAttr collection with max 100 documents in AppAttr collection
//
var tstRefHdr DataHdr

func nxtTestUserAccess(ctx context.Context) {

	var res [500]bool // 5 * 100 max
	var users *[]bson.M

	users = nxtReadUserAttrCollection(ctx) // user attributes from mongoDB

	idx := 2 // Use case for access policy
	ucase := opaUseCases[idx]
	tsm := TStateMap[ucase]
	for _, val := range *users {

		// skip header document and spec document for extended attributes
		uid := fmt.Sprintf("%s", val["_id"])
		if (uid == hdrKeyNm2[0]) || (uid == userExtAttrDocKey) {
			continue
		}

		// Evaluate query for each user trying to access each app bundle
		//
		for k := 0; k < tsm.Count; k = k + 1 {
			if tsm.Keys[k] == hdrKeyNm[idx] {
				continue
			}
			res[k] = nxtEvalAppAccessAuthz(ucase, nxtConvertToJSON(val), tsm.Keys[k])
			nxtLogInfo(uid+" accessing "+tsm.Keys[k], fmt.Sprintf("Result = %v", res[k]))
		}
	}
}

func nxtTestUserRouting(ctx context.Context) {

	var res [500]string // 5 * 100 max
	var users *[]bson.M

	hdr := make(http.Header, maxExtUAttr)
	aval := make(map[string]string, maxExtUAttr)

	aval["devOS"] = "MacOS"
	aval["osver"] = "14.1"
	aval["loc"] = "SJC"

	for idx, val := range extUAttr {
		hdr.Add(fmt.Sprintf("%s", val), aval[idx])
	}

	users = nxtReadUserAttrCollection(ctx) // user requests from mongoDB

	idx := 3 // Use case for user routing
	ucase := opaUseCases[idx]
	tsm := TStateMap[ucase]
	for _, val := range *users {

		// Ignore header doc and extended (runtime) attributes doc
		uid := fmt.Sprintf("%s", val["_id"])
		if (uid == hdrKeyNm2[0]) || (uid == userExtAttrDocKey) {
			continue
		}

		// Evaluate query for each user trying to access each app
		//
		for k := 0; k < tsm.Count; k = k + 1 {
			if tsm.Keys[k] == hdrKeyNm[idx] {
				continue
			}
			user := fmt.Sprintf("%s", val[kuser])
			res[k] = nxtEvalUserRouting(ucase, user, tsm.Keys[k], &hdr)
			nxtLogInfo(uid+" accessing "+tsm.Keys[k], fmt.Sprintf("Result = %v", res[k]))
		}
	}
}

// Read header and user attr documents from collection. Build input from query for
// each user document.
func nxtReadUserAttrCollection(ctx context.Context) *[]bson.M {
	tstRefHdr = nxtReadUserAttrHdr(ctx)
	nxtReadUserExtAttrDoc(ctx)
	return nxtReadAllUserAttrDocuments(ctx)
}

// Read user attr data from mongoDB collection and return bytes read
func nxtReadAllUserAttrDocuments(ctx context.Context) *[]bson.M {

	var users []bson.M

	coll := CollMap[userAttrCollection]
	cursor, err := coll.Find(ctx, bson.M{})
	if err != nil {
		nxtLogError("All users", fmt.Sprintf("Failed to find user attributes docs - %v", err))
		nxtMongoError()
		return &users
	}
	if err = cursor.All(ctx, &users); err != nil {
		nxtLogError("All users", fmt.Sprintf("Failed to read user attributes docs - %v", err))
		return &users
	}

	nusers := len(users)
	for i := 0; i < nusers; i++ {
		// Ignore header doc and extended (runtime) attributes doc
		uid := fmt.Sprintf("%s", users[i]["_id"])
		if (uid != hdrKeyNm2[0]) && (uid != userExtAttrDocKey) {
			// Change "_id" only for attribute docs, not header or ext attr spec
			users[i] = nxtFixupAttrID(users[i], kuser)
			users[i] = nxtAddVerToDoc(users[i], tstRefHdr)
		}
	}
	return &users
}

func nxtWriteAttrVersions() {
	qsm2 := QStateMap[opaUseCases[2]] // Access policy usecase
	qsm3 := QStateMap[opaUseCases[3]] // Route policy usecase
	versions := fmt.Sprintf("USER=%d.%d\nBUNDLE=%d.%d\nPOLICY=%d.%d\nROUTE=%d.%d",
		usrAttrHdr.Majver, usrAttrHdr.Minver, qsm2.RefHdr.Majver, qsm2.RefHdr.Minver,
		qsm2.PStruct.Majver, qsm2.PStruct.Minver, qsm3.RefHdr.Majver, qsm3.RefHdr.Minver)
	ioutil.WriteFile("/tmp/opa_attr_versions", []byte(versions), 0644)
	QStateMap[opaUseCases[0]].WrVer = false
	QStateMap[opaUseCases[2]].WrVer = false
	QStateMap[opaUseCases[3]].WrVer = false
}

//--------------------------------------End------------------------------------
