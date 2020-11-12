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
//     func NxtUsrJoin(userid string)
//     func NxtUsrLeave(userid string)
//     func NxtUsrAllowed(userid string) bool
//     <API for routing policy execution>
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
	"time"
	//"strings"

	"github.com/open-policy-agent/opa/rego"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive" // REMOTEDB
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

//
//--------------------------------Data Structures, Variables, etc ----------------------------------

const maxOpaUseCases = 5 // we currently have 4
const maxMongoColls = 10 // assume max 10 MongoDB collections, currently 6+1
const maxUsers = 10000   // max per tenant

/*****************************
// MongoDB database and collections
// TODO: Ensure each service pod gets the NxtDB for the tenant it is handling via
// a tenant specific DB name.
*****************************/
const nxtMongoDB = "NxtDB" // REMOTEDB
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

const agentldir = "authz/agent-authz"
const connldir = "authz/conn-authz"
const accessldir = "authz/app-access"
const routeldir = "authz/route-pol"

const agentauthzpolicy = "agent-authz.rego"
const connauthzpolicy = "conn-authz.rego"
const appaccesspolicy = "app-access.rego"
const userroutepolicy = "user-routing.rego"

const inpType = "UserAttr"
const inp2Type = "UserExtAttr"

const kmajver = "maj_ver"
const kminver = "min_ver"
const ktnt = "tenant"
const kbid = "bid"

// Common struct for all policies
// Can't seem to add a Rego policy via mongoshell, hence the hack to also
// provide a filename that is then used to read the policy from a local file.
type Policy struct {
	PolicyId string `json:"pid" bson:"_id"`
	Majver   int    `json:"majver" bson:"majver"` // major version
	Minver   int    `json:"minver" bson:"minver"` // minor version
	//Tenant   string `json:"tenant" bson:"tenant"` // tenant id
	Tenant primitive.ObjectID `json:"tenant" bson:"tenant"` // REMOTEDB
	Fname  string             `json:"fname" bson:"fname"`   // rego policy filename
	Rego   []rune             `json:"rego" bson:"rego"`     // rego policy
}

// Header document for a data collection so that the versions and tenant are not
// replicated in every document. The header can be read upfront to get the version
// info for the collection from a single document.
type DataHdr struct {
	ID     string `bson:"_id" json:"ID"`
	Majver int    `bson:"majver" json:"majver"`
	Minver int    `bson:"minver" json:"minver"`
	Tenant string `bson:"tenant" json:"tenant"`
}

// Data object to track every use case
type QState struct {
	NewVer   bool                   // new version of policy or refdata
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
	DataType string                 // key for header document
	DColl    string                 // name of data collection
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
var initUseCase = []int{0, 0, 1, 0}
var policyType = []string{"AgentPolicy", "ConnPolicy", "AccessPolicy", "RoutePolicy"} // Keys
var dataType = []string{"UserInfo", "AppInfo", "AppAttr", "HostAttr"}                 // Header doc keys
var opaQuery = []string{AAuthzQry, CAuthzQry, AccessQry, RouteQry}
var loadDir = []string{agentldir, connldir, accessldir, routeldir}
var policyfile = []string{agentauthzpolicy, connauthzpolicy, appaccesspolicy, userroutepolicy}

var initDone = make(chan bool, 1)
var evalDone = make(chan bool, 1)
var libInitialized bool
var tenant string
var slog *zap.SugaredLogger
var st, sg, sm zap.Field // for tenant, gateway, module

var DColls = []string{userInfoCollection, connInfoCollection, appAttrCollection, hostAttrCollection}
var CollMap map[string]*mongo.Collection
var mongoClient *mongo.Client
var nxtMongoDBName string

/******************************** Calls from minion ******************/
//export NxtAaaInit
func NxtAaaInit(namespace string, mongouri string, sl *zap.SugaredLogger) int {
	err := nxtOpaInit(namespace, mongouri, sl)
	if err != nil {
		return 1
	}
	return 0
}

//export NxtUsrJoin
func NxtUsrJoin(userid string) {
	if libInitialized == false {
		return
	}
	_ = nxtGetUserAttrJSON(userid)
}

//export NxtUsrLeave
func NxtUsrLeave(userid string) {
	if libInitialized == false {
		return
	}
	nxtPurgeUserAttrJSON(userid)
}

//export NxtGetUsrAttr
func NxtGetUsrAttr(userid string) (string, bool) {
	if libInitialized == false {
		return "", false
	}
	return nxtGetUserAttrJSON(userid), true
}

//export NxtUsrAllowed
func NxtUsrAllowed(userid string) bool {
	if libInitialized == false {
		return false
	}
	return true
}

//export NxtAccessOk
func NxtAccessOk(bundleid string, userattr string) bool {
	if libInitialized == false {
		return false
	}
	return nxtEvalAppAccessAuthz(opaUseCases[2], userattr, bundleid)
}

// export NxtRoutePolicy
func NxtRoutePolicy(uuid string) string {
	// To be done after controller support is available and API parameters
	// are finalized
	return ""
}

const RouteTag = "tag"

//export NxtRouteLookup
func NxtRouteLookup(uid string, routeid string) string {
	// For parity with python version of minion
	var route bson.M
	var key = uid + ":" + routeid
	err := CollMap[RouteCollection].FindOne(
		context.TODO(),
		bson.M{"_id": key},
	).Decode(&route)
	if err == nil {
		return fmt.Sprintf("%s", route[RouteTag])
	}
	return ""
}

/*********************************************************************/

func authzMain() {
}

// For now, this function tests access for a number of users with each app bundle.
// In production, this will monitor for DB updates and pull in any modified documents
// to reinitialize any OPA stuff
func nxtOpaProcess(ctx context.Context) int {

	select {
	case <-initDone:
	}

	for {
		for i, ucase := range opaUseCases {
			if initUseCase[i] > 0 {
				nxtSetupUseCase(ctx, i, ucase)
			}
		}
		// Process if new version of UserAttr collection
		nxtProcessUserAttrChanges(ctx)

		// sleep(5 secs)
		time.Sleep(5 * 1000 * time.Millisecond)
	}

	evalDone <- true // Done with all evaluations
	return 0
}

//-------------------------------- Init Functions ----------------------------------
// API to init nxt OPA interface
//export nxtOpaInit
func nxtOpaInit(ns string, mongouri string, sl *zap.SugaredLogger) error {

	var err error

	if libInitialized {
		return nil
	}

	ctx := context.Background()
	slog = sl
	tenant = ns
	st = zap.String("Tenant", tenant)
	// TODO: need cluster name for initializing below
	sg = zap.String("GW", "sj-nextensio.net")
	sm = zap.String("Module", "NxtOPA")

	// TODO: nxtMongoDBName needs to be derived from ns
	nxtMongoDBName = nxtMongoDB

	mongoClient, err = nxtMongoDBInit(ctx, ns, mongouri)
	if err != nil {
		nxtLogError(nxtMongoDBName, fmt.Sprintf("DB init error - %v", err))
		return err
	}

	// Initialize for each OPA use case as required. Initialization involves reading the
	// associated policy and reference data document or collection, ensuring their major
	// version matches, and if so, loading them into the load directory before creating
	// the query object and preparing the query for evaluation.
	for i, ucase := range opaUseCases {
		nxtCreateOpaUseCase(ucase, policyType[i], dataType[i], loadDir[i],
			opaQuery[i], DColls[i])
		if initUseCase[i] > 0 { // Initialize now in Init function
			nxtSetupUseCase(ctx, i, ucase)
		}
	}
	// Read header document for user attributes collection.
	// Set up cache for user attributes.
	// Then read the extended (runtime) attributes spec doc
	userAttr = make(map[string]usrCache, maxUsers)
	usrAttrHdr = nxtReadUserAttrHdr(ctx)
	nxtReadUserExtAttrDoc(ctx)

	libInitialized = true
	go nxtOpaProcess(ctx)
	initDone <- true
	return nil
}

// Do everything needed to set up mongoDB access
func nxtMongoDBInit(ctx context.Context, ns string, mURI string) (*mongo.Client, error) {

	// Set client options
	mongoclientOptions := options.Client().ApplyURI(mURI)

	// Connect to MongoDB
	cl, err := mongo.Connect(ctx, mongoclientOptions)
	if err != nil {
		nxtLogError(nxtMongoDBName, fmt.Sprintf("DB connect failure - %v", err))
		return nil, err
	}

	// Check the connection
	err = cl.Ping(ctx, nil)
	if err != nil {
		_ = cl.Disconnect(ctx)
		nxtLogError(nxtMongoDBName, fmt.Sprintf("Connection closed. DB ping failure - %v", err))
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

func nxtGetMongoEnv(key string, defaultValue string) string {
	v := os.Getenv(key)
	if v == "" {
		v = defaultValue
	}
	return v
}

// Create use case for each query type
func nxtCreateOpaUseCase(ucase string, ptype string, dtype string, ldir string,
	opaqry string, dcoll string) {
	var NewState QState
	var NewTS TState
	NewState.QUCase = ucase
	NewState.PolType = ptype
	NewState.DataType = dtype
	NewState.LDir = ldir
	NewState.Qry = opaqry
	NewState.DColl = dcoll
	NewState.QError = true
	QStateMap[ucase] = &NewState
	TStateMap[ucase] = &NewTS
	nxtLogDebug(ucase, fmt.Sprintf("Use case created for policy %s, refdata %s", ptype, dtype))
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
		return
	}
	qs := QStateMap[usecase]
	if (policy.Majver > qs.PStruct.Majver) || (policy.Minver > qs.PStruct.Minver) {
		// New policy. Store it in QStateMap
		qs.PStruct = policy
		qs.NewVer = true
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
	err := CollMap[coll].FindOne(ctx, bson.M{"_id": qs.DataType}).Decode(&hdr)
	if err != nil {
		nxtLogError(ucase, fmt.Sprintf("Failed to find %s header doc - %v", qs.DataType, err))
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
		return true
	}
	return false
}

// Read refdata from mongoDB collection. All data documents are read and combined
// into a JSON string for feeding into OPA
func nxtReadRefDataDoc(ctx context.Context, ucase string) {

	switch QStateMap[ucase].DataType {
	case dataType[0]:
		return
	case dataType[1]:
		return
	case dataType[2]:
		QStateMap[ucase].RefData = nxtCreateCollJSON(ctx, ucase, "{ \"bundles\":  [")
		return
	case dataType[3]:
		QStateMap[ucase].RefData = nxtCreateCollJSON(ctx, ucase, "{ \"hosts\":  [")
		return
	}
}

//-------------------------------User Attribute Functions---------------------------------

// Cache of user attributes for all active users. Cache is updated whenever
// the collection version changes. Cache entries are purged when a user disconnects.
type usrCache struct {
	uajson string
}

var userAttr map[string]usrCache
var userAttrLock bool
var usrAttrHdr DataHdr

// If new version of user attribues collection is available, read the
// extended attributes spec doc and update the cache for active users
func nxtProcessUserAttrChanges(ctx context.Context) {
	tmphdr := nxtReadUserAttrHdr(ctx)
	if (tmphdr.Majver > usrAttrHdr.Majver) || (tmphdr.Minver > usrAttrHdr.Minver) {
		usrAttrHdr = tmphdr
		nxtReadUserExtAttrDoc(ctx)
		nxtUpdateUserAttrCache()
	}
}

// Read header doc from user attributes collection to get version info
func nxtReadUserAttrHdr(ctx context.Context) DataHdr {
	// read header document for user attr collection used as input
	var uahdr DataHdr
	var errhdr = DataHdr{inpType, 0, 0, ""}

	coll := CollMap[userAttrCollection]
	err := coll.FindOne(ctx, bson.M{"_id": inpType}).Decode(&uahdr)
	if err != nil {
		nxtLogError(inpType, fmt.Sprintf("Failed to find header doc - %v", err))
		return errhdr
	}
	return uahdr
}

// Read one user's attr data from mongoDB collection and return it together
// with the json version with header info added. Called when user connects to service pod.
//export nxtGetUserAttrJSON
func nxtGetUserAttrJSON(uuid string) string {
	var uaC usrCache

	ua, ok := nxtReadUserAttrCache(uuid)
	if ok {
		return ua // cached version
	}

	uastruct, ok := nxtReadUserAttrDB(uuid)
	if ok {
		ua = nxtConvertToJSON(uastruct)
		if userAttrLock != true {
			uaC.uajson = ua
			userAttr[uuid] = uaC
			nxtLogDebug(uuid, "Added attributes for user to local cache")
		}
		return ua
	}
	return ""
}

// Read a user attribute doc from local cache
func nxtReadUserAttrCache(uuid string) (string, bool) {
	if userAttrLock {
		nxtLogDebug(uuid, "Local cache locked while retieving attributes for user")
		return "", false // force a read from the DB
	}
	// Check in cache if user's attributes exist. If yes, return value.
	uaC, ok := userAttr[uuid]
	if ok == true {
		nxtLogDebug(uuid, "Retrieved attributes for user from local cache")
		return uaC.uajson, true
	}
	nxtLogDebug(uuid, "Failed to find attributes for user in local cache")
	return "", false
}

// Read a user attribute doc from the DB and add header document info
func nxtReadUserAttrDB(uuid string) (bson.M, bool) {
	var usera bson.M

	ctx := context.Background()

	// Read user attributes from DB, cache json version, and return it
	coll := CollMap[userAttrCollection]
	err := coll.FindOne(ctx, bson.M{"_id": uuid}).Decode(&usera)
	if err != nil {
		nxtLogError(uuid, fmt.Sprintf("Failed to find attributes doc for user - %v", err))
		return bson.M{}, false
	}
	nusera := nxtAddVerToDoc(&usera, usrAttrHdr)
	return nusera, true
}

// Remove user attributes for a user on disconnect
//export nxtPurgeUserAttrJSON
func nxtPurgeUserAttrJSON(uuid string) {
	if userAttrLock != true {
		delete(userAttr, uuid) // if locked, let it be
	}
}

// Update cache because version info changed for collection
func nxtUpdateUserAttrCache() {
	var uaC usrCache

	userAttrLock = true
	for id, _ := range userAttr {
		uastruct, _ := nxtReadUserAttrDB(id)
		uaC.uajson = nxtConvertToJSON(uastruct)
		userAttr[id] = uaC
	}
	userAttrLock = false
	nxtLogDebug("UserAttrCache", fmt.Sprintf("Updated %v entries in local cache", len(userAttr)))
}

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
	err := coll.FindOne(ctx, bson.M{"_id": inp2Type}).Decode(&uahdr)
	if err != nil {
		nxtLogError(inp2Type, fmt.Sprintf("Failed to read user extended attributes doc - %v", err))
		return
	}

	// Cache spec read from DB
	for k := range extUAttr {
		delete(extUAttr, k)
	}
	if err := json.Unmarshal([]byte(uahdr.Attrlist), &extUAttr); err != nil {
		nxtLogError(inp2Type, fmt.Sprintf("Unmarshal error for extended user attributes - %v", err))
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
func nxtCreateCollJSON(ctx context.Context, ucase string, istr string) []byte {

	var attrstr string
	var docs []bson.M

	qsm := QStateMap[ucase]
	tsm := TStateMap[ucase]
	coll := qsm.DColl
	cursor, err := CollMap[coll].Find(ctx, bson.M{})
	if err != nil {
		nxtLogError(ucase, fmt.Sprintf("Failed to find any app bundle attribute docs - %v", err))
		return []byte("")
	}
	if err = cursor.All(ctx, &docs); err != nil {
		nxtLogError(ucase, fmt.Sprintf("Read failure for app bundle attributes - %v", err))
		return []byte("")
	}

	attrstr = istr
	ndocs := len(docs)
	tsm.Count = 0
	addComma := false
	for i := 0; i < ndocs; i++ {

		if docs[i]["_id"] == qsm.DataType { // Version doc
			tsm.Keys[i] = qsm.DataType
			tsm.Count = tsm.Count + 1
			continue
		}

		// Convert map structure to json
		// Concatenate json strings for attributes of each app bundle
		tsm.Keys[i] = fmt.Sprintf("%s", docs[i]["_id"])
		tsm.Count = tsm.Count + 1
		docs[i] = nxtAddVerToDoc(&docs[i], qsm.RefHdr)
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
//
// Go return type rego.ResultSet not supported in an exported function, hence return bool
// instead of the complex struct rego.ResultSet.

//export nxtEvalAppAccessAuthz
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
	nxtLogError(ucase, fmt.Sprintf("Query execution failure for %s to %s", ua["_id"], bid))
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

//export nxtEvalUserRouting
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
		return ""
	}
	uajson := nxtGetUserAttrJSON(uid)
	ueajson := nxtGetUserAttrFromHTTP(uid, hdr)
	rs, ok := nxtExecOpaQry(nxtEvalUserRoutingJSON(host, uajson, ueajson), ucase)
	if ok {
		return (fmt.Sprintf("%v", rs[0].Expressions[0].Value))
	}
	nxtLogError(ucase, "Query execution failure for "+uid+" to "+host)
	return ""
}

func nxtEvalUserRoutingJSON(host string, uajson string, ueajson string) []byte {
	str1 := "{\"host\": \""
	str2 := "\", \"dbattr\": "
	str3 := ", \"dynattr\": "
	jsonResp := fmt.Sprintf("%s%s%s%s%s%s }", str1, host, str2, uajson, str3, ueajson)
	return []byte(jsonResp)
}

//---------------------------------Rego interface functions-----------------------------
// Prime the load directory with the policy file and the reference data file
func nxtPrimeLoadDir(ucase string) {

	dirname := QStateMap[ucase].LDir
	err := ioutil.WriteFile(dirname+"/policyfile.rego", QStateMap[ucase].RegoPol, 0644)
	if err != nil {
		nxtLogError(ucase, fmt.Sprintf("Policy loading in dir %s failed - %v", dirname, err))
		// TODO: Can we avoid this ?
		log.Fatal(err)
	}

	// Write reference data to load directory
	err = ioutil.WriteFile(dirname+"/refdata.json", QStateMap[ucase].RefData, 0644)
	if err != nil {
		nxtLogError(ucase, fmt.Sprintf("Refdata loading in dir %s failed - %v", dirname, err))
		log.Fatal(err)
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
func nxtAddVerToDoc(ua *bson.M, hdr DataHdr) bson.M {
	tmp := *ua
	tmp[kmajver] = hdr.Majver
	tmp[kminver] = hdr.Minver
	tmp[ktnt] = hdr.Tenant
	return tmp
}

func nxtConvertToJSON(inp bson.M) string {
	jsonResp, merr := json.Marshal(inp)
	if merr != nil {
		id := fmt.Sprintf("%s", inp["_id"])
		nxtLogError(id, fmt.Sprintf("JSON marshal error for user - %v", merr))
		return ""
	}
	return string(jsonResp)
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

	idx := 2
	ucase := opaUseCases[idx]
	tsm := TStateMap[ucase]
	for _, val := range *users {

		// skip header document and spec document for extended attributes
		uid := fmt.Sprintf("%s", val["_id"])
		if (uid == inpType) || (uid == inp2Type) {
			continue
		}

		// Evaluate query for each user trying at access each app bundle
		//
		for k := 0; k < tsm.Count; k = k + 1 {
			if tsm.Keys[k] == dataType[idx] {
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

	idx := 3
	ucase := opaUseCases[idx]
	tsm := TStateMap[ucase]
	for _, val := range *users {

		// Ignore header doc and extended (runtime) attributes doc
		uid := fmt.Sprintf("%s", val["_id"])
		if (uid == inpType) || (uid == inp2Type) {
			continue
		}

		// Evaluate query for each user trying to access each app
		//
		for k := 0; k < tsm.Count; k = k + 1 {
			if tsm.Keys[k] == dataType[idx] {
				continue
			}
			res[k] = nxtEvalUserRouting(ucase, uid, tsm.Keys[k], &hdr)
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
		log.Fatal(err)
	}
	if err = cursor.All(ctx, &users); err != nil {
		log.Fatal(err)
	}

	nusers := len(users)
	for i := 0; i < nusers; i++ {
		// Ignore header doc and extended (runtime) attributes doc
		uid := fmt.Sprintf("%s", users[i]["_id"])
		if (uid != inpType) && (uid != inp2Type) {
			users[i] = nxtAddVerToDoc(&users[i], tstRefHdr)
		}
	}
	return &users
}

//--------------------------------------End------------------------------------
