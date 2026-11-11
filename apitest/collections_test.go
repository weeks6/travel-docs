package apitest

import (
	"net/http"
	"strings"
	"testing"
)

type collectionJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	OwnerID    string `json:"owner_id"`
	MyRole     string `json:"my_role"`
	RouteCount int    `json:"route_count"`
}

func (c *client) createCollection(name string, extra ...string) collectionJSON {
	c.e.t.Helper()
	body := map[string]string{"name": name}
	for _, kv := range extra {
		k, v, _ := strings.Cut(kv, "=")
		body[k] = v
	}
	var col collectionJSON
	c.do("POST", "/collections", body).expect(http.StatusCreated).decode(&col)
	return col
}

func TestCollections(t *testing.T) {
	e := setup(t)
	alice, bob, carol := e.register("alice"), e.register("bob"), e.register("carol")
	anon := e.anon()

	trip := alice.createCollection("Alps trip", "description=Summer 2026")
	if trip.MyRole != "owner" || trip.Visibility != "private" || trip.RouteCount != 0 {
		t.Errorf("created = %+v", trip)
	}
	alice.do("POST", "/collections", map[string]string{"name": " "}).expect(http.StatusBadRequest)
	alice.do("POST", "/collections", map[string]string{}).expect(http.StatusBadRequest)
	anon.do("POST", "/collections", map[string]string{"name": "x"}).expect(http.StatusUnauthorized)

	alicePrivate := alice.upload("day1", lineGPX("Day 1", 46, 8, 3, 10))
	bobPrivate := bob.upload("bob", lineGPX("Bob private", 46, 9, 3, 10))
	bobPublic := bob.upload("bobpub", lineGPX("Bob public", 46, 10, 3, 10), "visibility=public")

	// Adding routes: own routes and public routes only.
	path := "/collections/" + trip.ID + "/routes/"
	alice.do("PUT", path+alicePrivate.ID, nil).expect(http.StatusNoContent)
	alice.do("PUT", path+alicePrivate.ID, nil).expect(http.StatusNoContent) // idempotent
	alice.do("PUT", path+bobPublic.ID, nil).expect(http.StatusNoContent)
	alice.do("PUT", path+bobPrivate.ID, nil).expect(http.StatusNotFound)
	bob.do("PUT", path+bobPrivate.ID, nil).expect(http.StatusNotFound) // can't see the collection

	if l := alice.list("collection_id=" + trip.ID + "&sort=name&order=asc"); names(l.Items) != "Bob public, Day 1" {
		t.Errorf("collection routes = %q", names(l.Items))
	}

	// Private collection: invisible to others.
	bob.do("GET", "/collections/"+trip.ID, nil).expect(http.StatusNotFound)
	bob.do("GET", "/routes?collection_id="+trip.ID, nil).expect(http.StatusNotFound)

	// Sharing the collection shares the routes alice put in it.
	alice.do("PUT", "/collections/"+trip.ID+"/shares", map[string]string{"email": "bob@example.com", "role": "viewer"}).
		expect(http.StatusOK)
	var col collectionJSON
	bob.do("GET", "/collections/"+trip.ID, nil).expect(http.StatusOK).decode(&col)
	if col.MyRole != "viewer" || col.RouteCount != 2 {
		t.Errorf("bob sees %+v", col)
	}
	var r routeJSON
	bob.do("GET", "/routes/"+alicePrivate.ID, nil).expect(http.StatusOK).decode(&r)
	if r.MyRole != "viewer" {
		t.Errorf("route via collection role = %q", r.MyRole)
	}
	bob.do("PATCH", "/routes/"+alicePrivate.ID, map[string]string{"name": "x"}).expect(http.StatusForbidden)
	bob.do("PATCH", "/collections/"+trip.ID, map[string]string{"name": "x"}).expect(http.StatusForbidden)
	bob.do("PUT", path+bobPublic.ID, nil).expect(http.StatusForbidden)
	carol.do("GET", "/routes/"+alicePrivate.ID, nil).expect(http.StatusNotFound)

	var list struct {
		Items []collectionJSON `json:"items"`
		Total int              `json:"total"`
	}
	bob.do("GET", "/collections?scope=shared", nil).expect(http.StatusOK).decode(&list)
	if list.Total != 1 || list.Items[0].ID != trip.ID {
		t.Errorf("bob shared collections = %+v", list)
	}
	bob.do("GET", "/collections?scope=editable", nil).expect(http.StatusOK).decode(&list)
	if list.Total != 0 {
		t.Errorf("viewer must not see collection as editable")
	}

	// Editors can add their own routes, rename, but not change visibility/delete.
	alice.do("PUT", "/collections/"+trip.ID+"/shares", map[string]string{"email": "bob@example.com", "role": "editor"}).
		expect(http.StatusOK)
	bob.do("GET", "/collections?scope=editable", nil).expect(http.StatusOK).decode(&list)
	if list.Total != 1 {
		t.Errorf("editor should see collection as editable")
	}
	bob.do("PUT", path+bobPrivate.ID, nil).expect(http.StatusNoContent)
	bob.do("PATCH", "/collections/"+trip.ID, map[string]string{"name": "Alps trip (shared)"}).expect(http.StatusOK)
	bob.do("PATCH", "/collections/"+trip.ID, map[string]string{"visibility": "public"}).expect(http.StatusForbidden)
	bob.do("DELETE", "/collections/"+trip.ID, nil).expect(http.StatusForbidden)
	bob.do("GET", "/collections/"+trip.ID+"/shares", nil).expect(http.StatusForbidden)
	// Bob added his own private route, so alice (who can see the collection) sees it.
	alice.do("GET", "/routes/"+bobPrivate.ID, nil).expect(http.StatusOK)

	// Public collection: anyone sees it and the routes its contributors added.
	alice.do("PATCH", "/collections/"+trip.ID, map[string]string{"visibility": "public"}).expect(http.StatusOK)
	anon.do("GET", "/collections/"+trip.ID, nil).expect(http.StatusOK)
	anon.do("GET", "/routes/"+alicePrivate.ID, nil).expect(http.StatusOK)
	if l := anon.list("collection_id=" + trip.ID); l.Total != 3 {
		t.Errorf("anonymous collection listing = %q", names(l.Items))
	}

	// A public route added by someone else stops being visible through the
	// collection once its owner makes it private.
	bob.do("PATCH", "/routes/"+bobPublic.ID, map[string]string{"visibility": "private"}).expect(http.StatusOK)
	carol.do("GET", "/routes/"+bobPublic.ID, nil).expect(http.StatusNotFound)

	// Removing routes: collection editors, or the route's owner.
	carol.do("DELETE", path+alicePrivate.ID, nil).expect(http.StatusForbidden)
	bob.do("DELETE", path+bobPrivate.ID, nil).expect(http.StatusNoContent)
	alice.do("GET", "/routes/"+bobPrivate.ID, nil).expect(http.StatusNotFound)
	alice.do("DELETE", path+bobPrivate.ID, nil).expect(http.StatusNotFound)

	// Deleting the collection keeps the routes.
	alice.do("DELETE", "/collections/"+trip.ID, nil).expect(http.StatusNoContent)
	alice.do("GET", "/collections/"+trip.ID, nil).expect(http.StatusNotFound)
	alice.do("GET", "/routes/"+alicePrivate.ID, nil).expect(http.StatusOK)
	anon.do("GET", "/routes/"+alicePrivate.ID, nil).expect(http.StatusNotFound)
}

func TestCollectionListing(t *testing.T) {
	e := setup(t)
	alice, bob := e.register("alice"), e.register("bob")
	alice.createCollection("A private")
	pub := alice.createCollection("A public", "visibility=public")
	bob.createCollection("B private")
	route := alice.upload("r", sampleGPX)
	alice.do("PUT", "/collections/"+pub.ID+"/routes/"+route.ID, nil).expect(http.StatusNoContent)

	count := func(c *client, query string) int {
		var l struct {
			Total int `json:"total"`
		}
		c.do("GET", "/collections?"+query, nil).expect(http.StatusOK).decode(&l)
		return l.Total
	}
	if n := count(alice, ""); n != 2 {
		t.Errorf("alice mine = %d", n)
	}
	if n := count(bob, "scope=all"); n != 2 {
		t.Errorf("bob all = %d (own + public)", n)
	}
	if n := count(e.anon(), ""); n != 1 {
		t.Errorf("anonymous = %d", n)
	}
	if n := count(alice, "route_id="+route.ID); n != 1 {
		t.Errorf("collections containing route = %d", n)
	}
	e.anon().do("GET", "/collections?scope=mine", nil).expect(http.StatusUnauthorized)
	alice.do("GET", "/collections?scope=nope", nil).expect(http.StatusBadRequest)
}
