package policy

import (
	"context"
	"fmt"
	"net/http"

	"go.mongodb.org/mongo-driver/bson"
)

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
		if uid == HDRKEY {
			continue
		}

		// Evaluate query for each user trying to access each app bundle
		//
		for k := 0; k < tsm.Count; k = k + 1 {
			if tsm.Keys[k] == HDRKEY {
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

	hdr := make(http.Header)

	users = nxtReadUserAttrCollection(ctx) // user requests from mongoDB

	idx := 3 // Use case for user routing
	ucase := opaUseCases[idx]
	tsm := TStateMap[ucase]
	for _, val := range *users {

		// Ignore header doc and extended (runtime) attributes doc
		uid := fmt.Sprintf("%s", val["_id"])
		if uid == HDRKEY {
			continue
		}

		// Evaluate query for each user trying to access each app
		//
		for k := 0; k < tsm.Count; k = k + 1 {
			if tsm.Keys[k] == HDRKEY {
				continue
			}
			user := fmt.Sprintf("%s", val[kuser])
			res[k] = nxtEvalUserRouting("agent", ucase, user, tsm.Keys[k], &hdr)
			nxtLogInfo(uid+" accessing "+tsm.Keys[k], fmt.Sprintf("Result = %v", res[k]))
		}
	}
}

// Read header and user attr documents from collection. Build input from query for
// each user document.
func nxtReadUserAttrCollection(ctx context.Context) *[]bson.M {
	tstRefHdr = nxtReadUserAttrHdr(ctx)
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
		if uid != HDRKEY {
			// Change "_id" only for attribute docs, not header or ext attr spec
			users[i] = nxtFixupAttrID(users[i], kuser)
			users[i] = nxtAddVerToDoc(users[i], tstRefHdr)
		}
	}
	return &users
}
