package authz

/*************************************
// Nextensio interface for Opa Rego library to provide policy based application access
// authorization, Agent authorization, Connector authorization, etc.
// This code will be compiled together with the minion code running in a service pod.
// The minion code will first call nxtOpaInit() to set things up. After that, it will
// call an API specific to the authorization (or whatever) policy check required.
// Some APIs are to be called in ingress service pod, some in egress service pod.
// Common for every pod:
// NxtAAAInit(egress int) - to be called once for initialization before any other API calls
// Ingress pod APIs:
//     func NxtGetUsrAttr(userid string) (string, bool)
//     func NxtUsrJoin(userid string)
//     func NxtUsrLeave(userid string)
//     func NxtUsrAllowed(userid string) bool
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
	"os"
	"time"
	//"strings"

	"github.com/open-policy-agent/opa/rego"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive" // REMOTEDB
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

const maxOpaUseCases = 5 // we currently have 3
const maxMongoColls = 10 // assume max 10 MongoDB collections
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
const PolicyCollection = "NxtPolicies"

var opaUseCases = []string{"AgentAuthz", "ConnAuthz", "AppAccess"}
var initUseCase = []int{0, 0, 1}

var DColls = []string{userInfoCollection, connInfoCollection, appAttrCollection}
var CollMap map[string]*mongo.Collection
var mongoClient *mongo.Client
var nxtMongoDBName string

var initDone, evalDone chan bool
var inpType string
var libInitialized bool
var slog *zap.Logger
var tenant string

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
		// Monitoring for new version of UserAttr collection
		tmphdr := nxtReadUserAttrHdr(ctx)
		if (tmphdr.Majver > inpRefHdr.Majver) || (tmphdr.Minver > inpRefHdr.Minver) {
			inpRefHdr = tmphdr
			nxtUpdateUserAttrCache()
		}

		// sleep(5 secs)
		time.Sleep(5 * 1000 * time.Millisecond)
	}

	evalDone <- true // Done with all evaluations
	return 0
}

//
//-------------------------------- Init Functions ----------------------------------

// Can't seem to add a Rego policy via mongoshell, hence the temporary hack to
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
// replicated in every document, and the header can be read upfront to get the version
// info for the collection from a single document.
type DataHdr struct {
	ID     string `bson:"_id" json:"ID"`
	Majver int    `bson:"majver" json:"majver"`
	Minver int    `bson:"minver" json:"minver"`
	Tenant string `bson:"tenant" json:"tenant"`
}

// Data object to track every use case
type QState struct {
	NewVer   bool                   `bson:"newver" json:"newver"`   // new version of policy or refdata
	QCreated bool                   `bson:"loaded" json:"loaded"`   // query object created
	QError   bool                   `bson:"qerror" json:"qerror"`   // error in query state
	Qry      string                 `bson:"qry" json:"qry"`         // the OPA Rego query
	QUCase   string                 `bson:"qucase" json:"qucase"`   // query use case
	QryObj   *rego.Rego             `bson:"qryobj" json:"qryobj"`   // raw query
	PrepQry  rego.PreparedEvalQuery `bson:"prepqry" json:"prepqry"` // compiled query
	PolType  string                 `bson:"ptype" json:"ptype"`     // key for policy
	PStruct  Policy                 `bson:"pstruct" json:"pstruct"` // Policy struct
	RegoPol  []byte                 `bson:"regopol" json:"regopol"` // rego policy
	LDir     string                 `bson:"ldir" json:"ldir"`       // load directory for OPA
	DataType string                 `bson:"dtype" json:"dtype"`     // key for header document
	DColl    string                 `bson:"dcoll" json:"dcoll"`     // name of data collection
	RefHdr   DataHdr                `bson:"refhdr" json:"refhdr"`   // reference data header doc
	RefData  []byte                 `bson:"refdata" json:"refdata"` // reference data
}

var QStateMap map[string]*QState

const AAuthzQry = "data.app.access.allow"
const CAuthzQry = "data.app.access.allow"
const AccessQry = "data.app.access.allow"

const agentldir = "authz/agent-authz"
const connldir = "authz/conn-authz"
const accessldir = "authz/app-access"

const agentauthzpolicy = "agent-authz.rego"
const connauthzpolicy = "conn-authz.rego"
const appaccesspolicy = "app-access.rego"

var policyType = []string{"AgentPolicy", "ConnPolicy", "AccessPolicy"}
var dataType = []string{"UserInfo", "AppInfo", "AppAttr"}
var opaQuery = []string{AAuthzQry, CAuthzQry, AccessQry}
var loadDir = []string{agentldir, connldir, accessldir}
var policyfile = []string{agentauthzpolicy, connauthzpolicy, appaccesspolicy}

var st, sg, sm zap.Field

// API to init nxt OPA interface
//export nxtOpaInit
func nxtOpaInit(ns string, mongouri string, sl *zap.SugaredLogger) error {

	var err error

	if libInitialized {
		return nil
	}

	ctx := context.Background()
	slog = sl.Desugar()
	tenant = ns
	st = zap.String("Tenant", tenant)
	// TODO: need cluster name for initializing below
	sg = zap.String("GW", "sj-nextensio.net")
	sm = zap.String("Module", "NxtOPA")

	// TODO: nxtMongoDBName needs to be derived from ns
	nxtMongoDBName = nxtMongoDB

	evalDone = make(chan bool, 1)
	initDone = make(chan bool, 1)
	inpType = "UserAttr"

	QStateMap = make(map[string]*QState, maxOpaUseCases) // assume max 5 OPA use cases

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
	// Read header document for user attributes collection
	inpRefHdr = nxtReadUserAttrHdr(ctx)
	userAttr = make(map[string]string, maxUsers)

	libInitialized = true
	go nxtOpaProcess(ctx)
	initDone <- true
	return nil
}

func nxtLogError(ref string, msg string) {
	slog.Error(" ", st, sg, sm, zap.String("Ref", ref), zap.String("Msg", msg))
}

func nxtLogDebug(ref string, msg string) {
	slog.Debug(" ", st, sg, sm, zap.String("Ref", ref), zap.String("Msg", msg))
}

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
	nxtLogDebug(nxtMongoDBName, "DB being used for tenant")

	// Required on both apod and cpod
	CollMap[PolicyCollection] = db.Collection(PolicyCollection)
	// Required on cpod only
	CollMap[connInfoCollection] = db.Collection(connInfoCollection)
	CollMap[appAttrCollection] = db.Collection(appAttrCollection)
	// Required on apod only
	CollMap[userInfoCollection] = db.Collection(userInfoCollection)
	// Required on apod. Required on cpod for testing app-access authz
	CollMap[userAttrCollection] = db.Collection(userAttrCollection)

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
	NewState.QUCase = ucase
	NewState.PolType = ptype
	NewState.DataType = dtype
	NewState.LDir = ldir
	NewState.Qry = opaqry
	NewState.DColl = dcoll
	NewState.QError = true
	QStateMap[ucase] = &NewState
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
	nxtCheckUseCase(ctx, i, ucase)
}

func nxtCheckUseCase(ctx context.Context, i int, ucase string) {
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
	var policy []Policy

	// Read specific policy by specifying "_id" = ptype
	cursor, err := CollMap[PolicyCollection].Find(ctx, bson.M{"_id": ptype})
	if err != nil {
		nxtLogError(usecase, fmt.Sprintf("Failed to find %s", ptype))
		return
	}
	if err = cursor.All(ctx, &policy); err != nil {
		nxtLogError(usecase, fmt.Sprintf("%s read failure", ptype))
		return
	}

	if len(policy) <= 0 {
		nxtLogError(usecase, fmt.Sprintf("Read zero bytes of %s", ptype))
		return
	}
	pol := &policy[0]
	qs := QStateMap[usecase]
	if (pol.Majver > qs.PStruct.Majver) || (pol.Minver > qs.PStruct.Minver) {
		// New policy. Store it in QStateMap
		qs.PStruct = *pol
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
	bs, err := ioutil.ReadFile(QStateMap[ucase].PStruct.Fname)
	if err != nil {
		tstr := fmt.Sprintf("Local policy file %s read failure", QStateMap[ucase].PStruct.Fname)
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
	var hdr []DataHdr
	qs := QStateMap[ucase]
	coll := qs.DColl
	cursor, err := CollMap[coll].Find(ctx, bson.M{"_id": qs.DataType})
	if err != nil {
		nxtLogError(ucase, fmt.Sprintf("Failed to find header doc for %s", qs.DataType))
		return false
	}
	if err = cursor.All(ctx, &hdr); err != nil {
		nxtLogError(ucase, fmt.Sprintf("%s header doc read failure", qs.DataType))
		return false
	}

	if len(hdr) <= 0 {
		nxtLogError(ucase, fmt.Sprintf("Read zero bytes of %v header doc", qs.DataType))
		return false
	}

	// If data collection majver < policy document majver, ignore data collection and return
	if hdr[0].Majver < qs.PStruct.Majver {
		return false
	}
	// data collection majver >= policy document majver
	// if data collection majver or minver is newer than current version,
	// read entire data collection and store it in QStateMap
	if (hdr[0].Majver > qs.RefHdr.Majver) || (hdr[0].Minver > qs.RefHdr.Minver) {
		qs.RefHdr = hdr[0]
		qs.NewVer = true
		return true
	}
	return false
}

// Read refdata from mongoDB collection and return bytes read
func nxtReadRefDataDoc(ctx context.Context, ucase string) {

	switch QStateMap[ucase].DataType {
	case dataType[0]:
	case dataType[1]:
	case dataType[2]:
		QStateMap[ucase].RefData = nxtCreateBundleAttrCollJSON(ctx, ucase)
		return
	}
}

//-------------------------------User Attribute Functions---------------------------------
// User attributes + DataHdr added after read from collection.
type UserAttr struct {
	Uid      string   `bson:"_id" json:"uid"`
	Category string   `bson:"category" json:"category"` // "employee" or "nonemployee"
	Type     string   `bson:"type" json:"type"`         // "IC" or "manager" for employee
	Level    int      `bson:"level" json:"level"`       // IC or manager grade level
	Dept     []string `bson:"dept" json:"dept"`         // ["dept1", ...]
	Team     []string `bson:"team" json:"team"`         // ["team1, ...]
	Majver   int      `bson:"majver" json:"maj_ver"`
	Minver   int      `bson:"minver" json:"min_ver"`
	Tenant   string   `bson:"tenant" json:"tenant"`
}

// Bid added to above struct. Needed for input to opa.
type UserAttrPlusBid struct {
	Uid      string   `bson:"_id" json:"uid"`
	Category string   `bson:"category" json:"category"` // "employee" or "nonemployee"
	Type     string   `bson:"type" json:"type"`         // "IC" or "manager" for employee
	Level    int      `bson:"level" json:"level"`       // IC or manager grade level
	Dept     []string `bson:"dept" json:"dept"`         // ["dept1", ...]
	Team     []string `bson:"team" json:"team"`         // ["team1, ...]
	Majver   int      `bson:"majver" json:"maj_ver"`
	Minver   int      `bson:"minver" json:"min_ver"`
	Tenant   string   `bson:"tenant" json:"tenant"`
	Bid      string   `bson:"bid" json:"bid"` // target app bundle ID
}

var inpRefHdr DataHdr
var userAttr map[string]string
var userAttrLock bool

func nxtReadUserAttrHdr(ctx context.Context) DataHdr {

	// read header document for user attr collection used as input
	var uahdr []DataHdr
	var errhdr = DataHdr{inpType, 0, 0, ""}

	coll := CollMap[userAttrCollection]
	cursor, err := coll.Find(ctx, bson.M{"_id": inpType})
	if err != nil {
		nxtLogError(inpType, "Failed to find header doc")
		return errhdr
	}
	if err = cursor.All(ctx, &uahdr); err != nil {
		nxtLogError(inpType, "Header doc read failure")
		return errhdr
	}

	if len(uahdr) <= 0 {
		nxtLogError(inpType, "Read zero bytes of header doc")
		return errhdr
	}

	return uahdr[0]
}

// Read one user's attr data from mongoDB collection and return json version
// with header info added. Called when user connects to service pod.
//export nxtGetUserAttrJSON
func nxtGetUserAttrJSON(uuid string) string {
	ua, ok := nxtReadUserAttrCache(uuid)
	if ok {
		return ua // cached version
	}

	ua, ok = nxtReadUserAttrDB(uuid)
	if ok && (userAttrLock != true) {
		userAttr[uuid] = ua
		nxtLogDebug(uuid, "Added attributes for user to local cache")
	}
	return ua
}

func nxtReadUserAttrCache(uuid string) (string, bool) {
	if userAttrLock {
		// TODO: should we read from DB directly if cache locked ?
		nxtLogDebug(uuid, "Local cache locked while retieving attributes for user")
		return "", false // force a read from the DB
	}
	// Check in cache if user's attributes exist. If yes, return value.
	uaDoc, ok := userAttr[uuid]
	if ok == true {
		nxtLogDebug(uuid, "Retrieved attributes for user from local cache")
		return uaDoc, true
	}
	nxtLogDebug(uuid, "Failed to find attributes for user in local cache")
	return "", false
}

// Read a user attribute doc, add header document info, and convert it all to JSON
func nxtReadUserAttrDB(uuid string) (string, bool) {
	var usera []UserAttr

	ctx := context.Background()

	// Read user attributes from DB, cache json version, and return it
	coll := CollMap[userAttrCollection]
	cursor, err := coll.Find(ctx, bson.M{"_id": uuid})
	if err != nil {
		nxtLogError(uuid, "Failed to find attributes doc for user")
		return "", false
	}
	if err = cursor.All(ctx, &usera); err != nil {
		nxtLogError(uuid, "Attributes doc read failure for user")
		return "", false
	}
	if len(usera) <= 0 {
		nxtLogError(uuid, "Read zero bytes of attributes doc for user")
		return "", false
	}

	nxtAddVerToUserAttr(&usera[0])
	return nxtUserAttrJSON(&usera[0]), true // json string with version nfo
}

// Remove user attributes for a user on disconnect
//export nxtPurgeUserAttrJSON
func nxtPurgeUserAttrJSON(uuid string) {
	if userAttrLock != true {
		delete(userAttr, uuid) // if locked, let it be
	}
}

// We need this since the versions and tenant are in a separate header document
func nxtAddVerToUserAttr(ua *UserAttr) {

	ua.Majver = inpRefHdr.Majver
	ua.Minver = inpRefHdr.Minver
	ua.Tenant = inpRefHdr.Tenant
}

func nxtUserAttrJSON(user *UserAttr) string {
	jsonResp, merr := json.Marshal(user)
	if merr != nil {
		nxtLogError(user.Uid, "JSON marshal error for user")
		return ""
	}
	return string(jsonResp)
}

func nxtUpdateUserAttrCache() {
	userAttrLock = true
	for id, _ := range userAttr {
		userAttr[id], _ = nxtReadUserAttrDB(id)
	}
	userAttrLock = false
	nxtLogDebug("UserAttrCache", fmt.Sprintf("Updated %v entries in local cache", len(userAttr)))
}

//
//--------------------------App Bundle Attributes functions--------------------------

// Schema of document in NxtAppAttr Collection
type AppAttr struct {
	Bid         string   `bson:"_id" json:"bid"`
	Team        []string `bson:"team" json:"team"`               // ["team1", "team2", "team3", ...]
	Dept        []string `bson:"dept" json:"dept"`               // ["dept1", "dept2", "dept3", ...]
	Contrib     int      `bson:"IC" json:"IC"`                   // Minimum IC grade level for access
	Manager     int      `bson:"manager" json:"manager"`         // Minimum Manager grade level for access
	Nonemployee string   `bson:"nonemployee" json:"nonemployee"` // "allow" or "deny" for now
}

// Combo of AppAttr + DataHdr that is fed as refdata to OPA
// All App Bundle Attribute documents for a tenant are concatenated together to be fed
// to OPA as reference data where required
type bundleAttr struct {
	Bid         string   `bson:"_id" json:"bid"`
	Team        []string `bson:"team" json:"team"`
	Dept        []string `bson:"dept" json:"dept"`
	Contrib     int      `bson:"IC" json:"IC"`
	Manager     int      `bson:"manager" json:"manager"`
	Nonemployee string   `bson:"nonemployee" json:"nonemployee"`
	Majver      int      `bson:"majver" json:"maj_ver"`
	Minver      int      `bson:"minver" json:"min_ver"`
	Tenant      string   `bson:"tenant" json:"tenant"`
}

// Read all app bundle attribute records (documents) from collection in DB
// Add header document fields (versions, tenant, ...) to each attribute doc
// Convert to json and return a consolidated attributes file (collection)
func nxtCreateBundleAttrCollJSON(ctx context.Context, ucase string) []byte {

	var attrstr string
	var bundles []bundleAttr

	coll := QStateMap[ucase].DColl
	cursor, err := CollMap[coll].Find(ctx, bson.M{})
	if err != nil {
		nxtLogError(ucase, "Failed to find any app bundle attribute docs")
		return []byte("")
	}
	if err = cursor.All(ctx, &bundles); err != nil {
		nxtLogError(ucase, "Read failure for app bundle attributes")
		return []byte("")
	}

	attrstr = "{ \"bundles\":  ["
	nbun := len(bundles)
	testBidCnt = 0
	addComma := false
	for i := 0; i < nbun; i++ {

		if bundles[i].Bid == QStateMap[ucase].DataType { // Version doc
			testBids[i] = QStateMap[ucase].DataType
			testBidCnt = testBidCnt + 1
			continue
		}

		// Convert Go structure to json
		// Concatenate json strings for attributes of each app bundle
		testBids[i] = bundles[i].Bid
		testBidCnt = testBidCnt + 1
		nxtAddVerToBundleAttrDoc(ucase, &bundles[i])
		if addComma == true {
			attrstr = attrstr + ",\n"
		}
		attrstr = attrstr + nxtBundleAttrDocJSON(&bundles[i])
		addComma = true
	}
	attrstr = attrstr + "\n]\n}"
	return []byte(attrstr)
}

func nxtBundleAttrDocJSON(bun *bundleAttr) string {
	jsonResp, merr := json.Marshal(bun)
	if merr != nil {
		nxtLogError(bun.Bid, "JSON marshal error for app bundle")
		return ""
	}
	return string(jsonResp)
}

// We need this since the versions and tenant are in a separate header document
func nxtAddVerToBundleAttrDoc(ucase string, ba *bundleAttr) {

	ba.Majver = QStateMap[ucase].RefHdr.Majver
	ba.Minver = QStateMap[ucase].RefHdr.Minver
	ba.Tenant = QStateMap[ucase].RefHdr.Tenant
}

//
//------------------------------ App Bundle Access Authz --------------------------------
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
	var ua UserAttrPlusBid

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
	ua.Bid = bid
	return nxtEvalAppAccessAuthzCore(nxtUserAttrPlusBidJSON(&ua), ucase)
}

func nxtEvalAppAccessAuthzCore(inp []byte, ucase string) bool {
	// Rego object is pre-created and query prepared for evaluation.
	// Here we only evaluate the prepared query with the input data

	var input interface{}

	ctx := context.Background()

	if err := json.Unmarshal(inp, &input); err != nil {
		nxtLogError(ucase, fmt.Sprintf("Eval input JSON unmarshal error - %v", err))
		return false
	}

	// for each prepared query, execute the evaluation.
	rs, err := QStateMap[ucase].PrepQry.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		nxtLogError(ucase, fmt.Sprintf("Evaluation error - %v", err))
		return false
	}
	if rs == nil {
		nxtLogError(ucase, fmt.Sprintf("Evaluation with Nil result - %v", err))
		return false
	}
	retval := fmt.Sprintf("%v", rs[0].Expressions[0].Value)
	return retval == "true"
}

func nxtUserAttrPlusBidJSON(user *UserAttrPlusBid) []byte {
	jsonResp, merr := json.Marshal(user)
	if merr != nil {
		nxtLogError(user.Uid, fmt.Sprintf("JSON marshal error for user", merr))
		return []byte("")
	}
	return jsonResp
}

//
//------------------------------- Agent Authz -----------------------------------

func nxtEvalAgentAuthz(ctx context.Context, ldir string, inp []byte) bool {

	//
	// ldir is a directory containing the policy and the user info record
	// inp is the Input from the "hello" packet received from Agent
	// For Agent authz, create Rego object, prepare query for eval, and evaluate in one stroke
	//

	QS := QStateMap[opaUseCases[0]]
	r := nxtCreateOpaQry(QS.Qry, QS.LDir)

	// Create a prepared query that can be evaluated.
	pquery, qerr := nxtPrepOpaQry(ctx, r, opaUseCases[0])
	QS.QError = qerr

	// var res bool
	var input interface{}

	if err := json.Unmarshal(inp, &input); err != nil {
		nxtLogError("AgentAuthz", fmt.Sprintf("JSON unmarshal error for input - %v", err))
	}

	// for each prepared query, execute the evaluation.
	rs, err := pquery.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		nxtLogError("AgentAuthz", fmt.Sprintf("Evaluation error - %v", err))
	}
	if rs == nil {
		nxtLogError("AgentAuthz", "Evaluation Nil result error")
	}
	retval := fmt.Sprintf("%v", rs[0].Expressions[0].Value)
	return retval == "true"
}

//
//---------------------------- Connector Authz -------------------------------

func nxtEvalConnectorAuthz(ctx context.Context, inp []byte) bool {

	//
	// ldir is a directory containing the policy and the app bundle info file
	// inp is the Input from the "hello" packet received from Connector
	// Rego object is pre-created and query prepared for evaluation.
	// Here we only evaluate the prepared query with the "hello" data
	//

	var input interface{}

	if err := json.Unmarshal(inp, &input); err != nil {
		nxtLogError("ConnAuthz", fmt.Sprintf("JSON unmarshal error for input - %v", err))
	}

	// for each prepared query, execute the evaluation.
	rs, err := QStateMap[opaUseCases[1]].PrepQry.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		nxtLogError("ConnAuthz", fmt.Sprintf("Evaluation error - %v", err))
	}
	if rs == nil {
		nxtLogError("ConnAuthz", "Evaluation Nil result error")
	}
	retval := fmt.Sprintf("%v", rs[0].Expressions[0].Value)
	return retval == "true"
}

//---------------------------------Rego interface functions-----------------------------
// Prime the load directory with the policy file and the reference data file
func nxtPrimeLoadDir(ucase string) {

	dirname := QStateMap[ucase].LDir
	err := ioutil.WriteFile(dirname+"/policyfile.rego", QStateMap[ucase].RegoPol, 0644)
	if err != nil {
		nxtLogError(ucase, fmt.Sprintf("Error while loading policy in dir %s", dirname))
		// TODO: Can we avoid this ?
		log.Fatal(err)
	}

	// Write reference data to load directory
	//
	err = ioutil.WriteFile(dirname+"/refdata.json", QStateMap[ucase].RefData, 0644)
	if err != nil {
		nxtLogError(ucase, fmt.Sprintf("Error while loading refdata in dir %s", dirname))
		log.Fatal(err)
	}
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

//-----------------------------------------Test functions-------------------------------
// Test function for application access
// It tests a combination of users with app bundles :
// a) 5 users with max 100 documents populated in mongoDB AppAttr collection
// b) documents in mongoDB UserAttr collection with max 100 documents in AppAttr collection
//
var testBids [100]string
var testBidCnt int

func nxtTestUserAccess(ctx context.Context) {

	var res [500]bool // 5 * 100 max
	var users []UserAttr

	users = nxtReadUserAttrCollection(ctx) // user attributes from mongoDB

	for _, val := range users {

		if val.Uid == inpType { // skip header document
			continue
		}

		// Evaluate query for each user trying at access each app bundle
		//
		for k := 0; k < testBidCnt; k = k + 1 {
			if testBids[k] == "AppAttr" {
				continue
			}
			res[k] = nxtEvalAppAccessAuthz(opaUseCases[2], nxtUserAttrJSON(&val), testBids[k])
			nxtLogDebug(val.Uid+" accessing "+testBids[k], fmt.Sprintf("Result = %v", res[k]))
		}
	}
}

// Read header and user attr documents from collection. Build input from query for
// each user document.
func nxtReadUserAttrCollection(ctx context.Context) []UserAttr {
	inpRefHdr = nxtReadUserAttrHdr(ctx)
	return nxtReadAllUserAttrDocuments(ctx)
}

// Read user attr data from mongoDB collection and return bytes read
func nxtReadAllUserAttrDocuments(ctx context.Context) []UserAttr {

	var users []UserAttr

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
		if users[i].Uid != inpType { // Ignore version document
			nxtAddVerToUserAttr(&users[i])
		}
	}
	return users
}

//--------------------------------------End------------------------------------
