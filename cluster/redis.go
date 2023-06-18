package cluster

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/garyburd/redigo/redis"

	"github.com/mu-box/portal/balance"
	"github.com/mu-box/portal/config"
	"github.com/mu-box/portal/core"
	"github.com/mu-box/portal/core/common"
	"github.com/mu-box/portal/database"
)

var (
	self string
	ttl  = 20 // time until a member is deemed "dead"
	beat = time.Duration(ttl/2) * time.Second
	pool *redis.Pool
)

type (
	Redis struct {
		subconn redis.PubSubConn
	}
)

func (r *Redis) Init() error {
	hostname, _ := os.Hostname()
	self = fmt.Sprintf("%s:%s", hostname, config.ApiPort)
	pool = r.newPool(config.ClusterConnection, config.ClusterToken)

	// get vips
	vips, err := r.GetVips()
	if err != nil {
		return fmt.Errorf("Failed to get vips - %s", err)
	}
	// write vips
	if vips != nil {
		config.Log.Trace("[cluster] - Setting vips...")
		err = common.SetVips(vips)
		if err != nil {
			return fmt.Errorf("Failed to set vips - %s", err)
		}
	}

	// get services
	services, err := r.GetServices()
	if err != nil {
		return fmt.Errorf("Failed to get services - %s", err)
	}
	// write services
	if services != nil {
		config.Log.Trace("[cluster] - Setting services...")
		err = common.SetServices(services)
		if err != nil {
			return fmt.Errorf("Failed to set services - %s", err)
		}
	}

	// get routes
	routes, err := r.GetRoutes()
	if err != nil {
		return fmt.Errorf("Failed to get routes - %s", err)
	}
	// write routes
	if routes != nil {
		config.Log.Trace("[cluster] - Setting routes...")
		err = common.SetRoutes(routes)
		if err != nil {
			return fmt.Errorf("Failed to set routes - %s", err)
		}
	}

	// get certs
	certs, err := r.GetCerts()
	if err != nil {
		return fmt.Errorf("Failed to get certs - %s", err)
	}
	// write certs
	if certs != nil {
		config.Log.Trace("[cluster] - Setting certs...")
		err = common.SetCerts(certs)
		if err != nil {
			return fmt.Errorf("Failed to set certs - %s", err)
		}
	}

	// note: keep subconn connection initialization out here or sleep after `go r.subscribe()`
	// don't set read timeout on subscriber - it dies if no 'updates' within that time
	s, err := redis.DialURL(config.ClusterConnection, redis.DialConnectTimeout(30*time.Second), redis.DialPassword(config.ClusterToken))
	if err != nil {
		return fmt.Errorf("Failed to reach redis for subconn - %s", err)
	}

	r.subconn = redis.PubSubConn{s}
	r.subconn.Subscribe("portal")

	p := pool.Get()
	defer p.Close()

	p.Do("SET", self, "alive", "EX", ttl)
	_, err = p.Do("SADD", "members", self)
	if err != nil {
		return fmt.Errorf("Failed to add myself to list of members - %s", err)
	}

	go r.subscribe()
	go r.heartbeat()
	go r.cleanup()

	return nil
}

////////////////////////////////////////////////////////////////////////////////
// SERVICES
////////////////////////////////////////////////////////////////////////////////

// SetServices tells all members to replace the services in their database with a new set.
// rolls back on failure
func (r *Redis) SetServices(services []core.Service) error {
	conn := pool.Get()
	defer conn.Close()

	oldServices, err := common.GetServices()
	if err != nil {
		return err
	}

	// publishJson to others
	err = r.publishJson(conn, "set-services", services)
	if err != nil {
		// if i failed to publishJson, request should fail
		return err
	}

	actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-services %s", services))))

	// ensure all members applied action
	err = r.waitForMembers(conn, actionHash)
	if err != nil {
		uActionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-services %s", oldServices))))
		// cleanup rollback cruft. clear actionHash ensures no mistakes on re-submit
		defer conn.Do("DEL", uActionHash, actionHash)
		// attempt rollback - no need to waitForMembers here
		uerr := r.publishJson(conn, "set-services", oldServices)
		if uerr != nil {
			err = fmt.Errorf("%s - %s", err, uerr)
		}
		return err
	}

	if database.CentralStore {
		return database.SetServices(services)
	}

	return nil
}

// SetService tells all members to add the service to their database.
// rolls back on failure
func (r *Redis) SetService(service *core.Service) error {
	conn := pool.Get()
	defer conn.Close()

	// publishJson to others
	err := r.publishJson(conn, "set-service", service)
	if err != nil {
		// nothing to rollback yet (nobody received)
		return err
	}

	actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-service %s", *service))))

	// ensure all members applied action
	err = r.waitForMembers(conn, actionHash)
	if err != nil {
		uActionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("delete-service %s", service.Id))))
		// cleanup rollback cruft. clear actionHash ensures no mistakes on re-submit
		defer conn.Do("DEL", uActionHash, actionHash)
		// attempt rollback - no need to waitForMembers here
		uerr := r.publishString(conn, "delete-service", service.Id)
		if uerr != nil {
			err = fmt.Errorf("%s - %s", err, uerr)
		}
		return err
	}

	if database.CentralStore {
		return database.SetService(service)
	}

	return nil
}

// DeleteService tells all members to remove the service from their database.
// rolls back on failure
func (r *Redis) DeleteService(id string) error {
	conn := pool.Get()
	defer conn.Close()

	oldService, err := common.GetService(id)
	// this should not return nil to ensure the service is gone from entire cluster
	if err != nil && !strings.Contains(err.Error(), "No Service Found") {
		return err
	}

	// publishString to others
	err = r.publishString(conn, "delete-service", id)
	if err != nil {
		// if i failed to publishString, request should fail
		return err
	}

	actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("delete-service %s", id))))

	// ensure all members applied action
	err = r.waitForMembers(conn, actionHash)
	if err != nil {
		uActionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-service %s", oldService))))
		// cleanup rollback cruft. clear actionHash ensures no mistakes on re-submit
		defer conn.Do("DEL", uActionHash, actionHash)
		// attempt rollback - no need to waitForMembers here
		uerr := r.publishJson(conn, "set-service", oldService)
		if uerr != nil {
			err = fmt.Errorf("%s - %s", err, uerr)
		}
		return err
	}

	if database.CentralStore {
		return database.DeleteService(id)
	}

	return nil
}

////////////////////////////////////////////////////////////////////////////////
// SERVERS
////////////////////////////////////////////////////////////////////////////////

// SetServers tells all members to replace a service's servers with a new set.
// rolls back on failure
func (r *Redis) SetServers(svcId string, servers []core.Server) error {
	conn := pool.Get()
	defer conn.Close()

	service, err := common.GetService(svcId)
	if err != nil {
		return NoServiceError
	}
	oldServers := service.Servers

	// publishStringJson to others
	err = r.publishStringJson(conn, "set-servers", svcId, servers)
	if err != nil {
		return err
	}

	actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-servers %s %s", servers, svcId))))

	// ensure all members applied action
	err = r.waitForMembers(conn, actionHash)
	if err != nil {
		uActionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-servers %s %s", oldServers, svcId))))
		// cleanup rollback cruft. clear actionHash ensures no mistakes on re-submit
		defer conn.Do("DEL", uActionHash, actionHash)
		// attempt rollback - no need to waitForMembers here
		uerr := r.publishStringJson(conn, "set-servers", svcId, oldServers)
		if uerr != nil {
			err = fmt.Errorf("%s - %s", err, uerr)
		}
		return err
	}

	if database.CentralStore {
		return database.SetServers(svcId, servers)
	}

	return nil
}

// SetServer tells all members to add the server to their database.
// rolls back on failure
func (r *Redis) SetServer(svcId string, server *core.Server) error {
	conn := pool.Get()
	defer conn.Close()

	// publishStringJson to others
	err := r.publishStringJson(conn, "set-server", svcId, server)
	if err != nil {
		return err
	}

	actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-server %s %s", *server, svcId))))

	// ensure all members applied action
	err = r.waitForMembers(conn, actionHash)
	if err != nil {
		uActionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("delete-server %s %s", server.Id, svcId))))
		// cleanup rollback cruft. clear actionHash ensures no mistakes on re-submit
		defer conn.Do("DEL", uActionHash, actionHash)
		// attempt rollback - no need to waitForMembers here
		uerr := r.publishStringJson(conn, "delete-server", server.Id, svcId)
		if uerr != nil {
			err = fmt.Errorf("%s - %s", err, uerr)
		}
		return err
	}

	if database.CentralStore {
		return database.SetServer(svcId, server)
	}

	return nil
}

// DeleteServer tells all members to remove the server from their database.
// rolls back on failure
func (r *Redis) DeleteServer(svcId, srvId string) error {
	conn := pool.Get()
	defer conn.Close()

	oldServer, err := common.GetServer(svcId, srvId)
	// mustn't return nil here to ensure cluster removes the server
	if err != nil && !strings.Contains(err.Error(), "No Server Found") {
		return err
	}

	// publishStringJson to others
	// todo: swap srv/svc ids to match backender interface for better readability
	err = r.publishString(conn, "delete-server", srvId, svcId)
	if err != nil {
		return err
	}

	actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("delete-server %s %s", srvId, svcId))))

	// ensure all members applied action
	err = r.waitForMembers(conn, actionHash)
	if err != nil {
		uActionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-server %s %s", *oldServer, svcId))))
		// cleanup rollback cruft. clear actionHash ensures no mistakes on re-submit
		defer conn.Do("DEL", uActionHash, actionHash)
		// attempt rollback - no need to waitForMembers here
		uerr := r.publishStringJson(conn, "set-server", svcId, oldServer)
		if uerr != nil {
			err = fmt.Errorf("%s - %s", err, uerr)
		}
		return err
	}

	if database.CentralStore {
		return database.DeleteServer(svcId, srvId)
	}

	return nil
}

////////////////////////////////////////////////////////////////////////////////
// ROUTES
////////////////////////////////////////////////////////////////////////////////

// SetRoutes tells all members to replace the routes in their database with a new set.
// rolls back on failure
func (r Redis) SetRoutes(routes []core.Route) error {
	conn := pool.Get()
	defer conn.Close()

	oldRoutes, err := common.GetRoutes()
	if err != nil {
		return err
	}

	// publishJson to others
	err = r.publishJson(conn, "set-routes", routes)
	if err != nil {
		// if i failed to publishJson, request should fail
		return err
	}

	actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-routes %s", routes))))

	// ensure all members applied action
	err = r.waitForMembers(conn, actionHash)
	if err != nil {
		uActionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-routes %s", oldRoutes))))
		// cleanup rollback cruft. clear actionHash ensures no mistakes on re-submit
		defer conn.Do("DEL", uActionHash, actionHash)
		// attempt rollback - no need to waitForMembers here
		uerr := r.publishJson(conn, "set-routes", oldRoutes)
		if uerr != nil {
			err = fmt.Errorf("%s - %s", err, uerr)
		}
		return err
	}

	if database.CentralStore {
		return database.SetRoutes(routes)
	}

	return nil
}

// SetRoute tells all members to add the route to their database.
// rolls back on failure
func (r Redis) SetRoute(route core.Route) error {
	conn := pool.Get()
	defer conn.Close()

	// publishJson to others
	err := r.publishJson(conn, "set-route", route)
	if err != nil {
		// nothing to rollback yet (nobody received)
		return err
	}

	actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-route %s", route))))

	// ensure all members applied action
	err = r.waitForMembers(conn, actionHash)
	if err != nil {
		uActionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("delete-route %s", route))))
		// cleanup rollback cruft. clear actionHash ensures no mistakes on re-submit
		defer conn.Do("DEL", uActionHash, actionHash)
		// attempt rollback - no need to waitForMembers here
		uerr := r.publishJson(conn, "delete-route", route)
		if uerr != nil {
			err = fmt.Errorf("%s - %s", err, uerr)
		}
		return err
	}

	if database.CentralStore {
		return database.SetRoute(route)
	}

	return nil
}

// DeleteRoute tells all members to remove the route from their database.
// rolls back on failure
func (r Redis) DeleteRoute(route core.Route) error {
	conn := pool.Get()
	defer conn.Close()

	oldRoutes, err := common.GetRoutes()
	// this should not return nil to ensure the route is gone from entire cluster
	if err != nil && !strings.Contains(err.Error(), "No Route Found") {
		return err
	}

	// publishJson to others
	err = r.publishJson(conn, "delete-route", route)
	if err != nil {
		// if i failed to publishJson, request should fail
		return err
	}

	actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("delete-route %s", route))))

	// ensure all members applied action
	err = r.waitForMembers(conn, actionHash)
	if err != nil {
		uActionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-routes", oldRoutes))))
		// cleanup rollback cruft. clear actionHash ensures no mistakes on re-submit
		defer conn.Do("DEL", uActionHash, actionHash)
		// attempt rollback - no need to waitForMembers here
		uerr := r.publishJson(conn, "set-routes", oldRoutes)
		if uerr != nil {
			err = fmt.Errorf("%s - %s", err, uerr)
		}
		return err
	}

	if database.CentralStore {
		return database.DeleteRoute(route)
	}

	return nil
}

////////////////////////////////////////////////////////////////////////////////
// CERTS
////////////////////////////////////////////////////////////////////////////////

// SetCerts tells all members to replace the certs in their database with a new set.
// rolls back on failure
func (r Redis) SetCerts(certs []core.CertBundle) error {
	conn := pool.Get()
	defer conn.Close()

	oldCerts, err := common.GetCerts()
	if err != nil {
		return err
	}

	// publishJson to others
	err = r.publishJson(conn, "set-certs", certs)
	if err != nil {
		// if i failed to publishJson, request should fail
		return err
	}

	actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-certs %s", certs))))

	// ensure all members applied action
	err = r.waitForMembers(conn, actionHash)
	if err != nil {
		uActionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-certs %s", oldCerts))))
		// cleanup rollback cruft. clear actionHash ensures no mistakes on re-submit
		defer conn.Do("DEL", uActionHash, actionHash)
		// attempt rollback - no need to waitForMembers here
		uerr := r.publishJson(conn, "set-certs", oldCerts)
		if uerr != nil {
			err = fmt.Errorf("%s - %s", err, uerr)
		}
		return err
	}

	if database.CentralStore {
		return database.SetCerts(certs)
	}

	return nil
}

// SetCert tells all members to add the cert to their database.
// rolls back on failure
func (r Redis) SetCert(cert core.CertBundle) error {
	conn := pool.Get()
	defer conn.Close()

	// publishJson to others
	err := r.publishJson(conn, "set-cert", cert)
	if err != nil {
		// nothing to rollback yet (nobody received)
		return err
	}

	actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-cert %s", cert))))

	// ensure all members applied action
	err = r.waitForMembers(conn, actionHash)
	if err != nil {
		uActionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("delete-cert %s", cert))))
		// cleanup rollback cruft. clear actionHash ensures no mistakes on re-submit
		defer conn.Do("DEL", uActionHash, actionHash)
		// attempt rollback - no need to waitForMembers here
		uerr := r.publishJson(conn, "delete-cert", cert)
		if uerr != nil {
			err = fmt.Errorf("%s - %s", err, uerr)
		}
		return err
	}

	if database.CentralStore {
		return database.SetCert(cert)
	}

	return nil
}

// DeleteCert tells all members to remove the cert from their database.
// rolls back on failure
func (r Redis) DeleteCert(cert core.CertBundle) error {
	conn := pool.Get()
	defer conn.Close()

	oldCerts, err := common.GetCerts()
	// this should not return nil to ensure the cert is gone from entire cluster
	if err != nil && !strings.Contains(err.Error(), "No Cert Found") {
		return err
	}

	// publishJson to others
	err = r.publishJson(conn, "delete-cert", cert)
	if err != nil {
		// if i failed to publishJson, request should fail
		return err
	}

	actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("delete-cert %s", cert))))

	// ensure all members applied action
	err = r.waitForMembers(conn, actionHash)
	if err != nil {
		uActionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-certs", oldCerts))))
		// cleanup rollback cruft. clear actionHash ensures no mistakes on re-submit
		defer conn.Do("DEL", uActionHash, actionHash)
		// attempt rollback - no need to waitForMembers here
		uerr := r.publishJson(conn, "set-certs", oldCerts)
		if uerr != nil {
			err = fmt.Errorf("%s - %s", err, uerr)
		}
		return err
	}

	if database.CentralStore {
		return database.DeleteCert(cert)
	}

	return nil
}

////////////////////////////////////////////////////////////////////////////////
// VIPS
////////////////////////////////////////////////////////////////////////////////

// todo: clustered
func (r Redis) SetVips(vips []core.Vip) error {
	return common.SetVips(vips)
}
func (r Redis) SetVip(vip core.Vip) error {
	return common.SetVip(vip)
}
func (r Redis) DeleteVip(vip core.Vip) error {
	return common.DeleteVip(vip)
}
func (r Redis) GetVips() ([]core.Vip, error) {
	return common.GetVips()
}

////////////////////////////////////////////////////////////////////////////////
// GETS
////////////////////////////////////////////////////////////////////////////////

// GetService - likely will never be called
func (r *Redis) GetService(id string) (*core.Service, error) {
	return common.GetService(id)
}

// GetServices gets a list of services from the database, or another cluster member.
func (r *Redis) GetServices() ([]core.Service, error) {
	if database.CentralStore {
		return database.GetServices()
	}

	conn := pool.Get()
	defer conn.Close()

	// get known members(other than me) to 'poll' for services
	members, _ := redis.Strings(conn.Do("SMEMBERS", "members"))
	if len(members) == 0 {
		// should only happen on new cluster
		// assume i'm ok to be primary so don't reset imported services
		config.Log.Trace("[cluster] - Assuming OK to be primary, using services from my database...")
		return common.GetServices()
	}
	for i := range members {
		if members[i] == self {
			// if i'm in the list of members, new requests should have failed while `waitForMembers`ing
			config.Log.Trace("[cluster] - Assuming I was in sync, using services from my database...")
			return common.GetServices()
		}
	}

	c, err := redis.DialURL(config.ClusterConnection, redis.DialConnectTimeout(15*time.Second), redis.DialPassword(config.ClusterToken))
	if err != nil {
		return nil, fmt.Errorf("Failed to reach redis for services subscriber - %s", err)
	}
	defer c.Close()

	message := make(chan interface{})
	subconn := redis.PubSubConn{c}

	// subscribe to channel that services will be published on
	if err := subconn.Subscribe("services"); err != nil {
		return nil, fmt.Errorf("Failed to reach redis for services subscriber - %s", err)
	}
	defer subconn.Close()

	// listen always
	go func() {
		for {
			message <- subconn.Receive()
		}
	}()

	// todo: maybe use ttl?
	// timeout is how long to wait for the listed members to come back online
	timeout := time.After(time.Duration(20) * time.Second)

	// loop attempts for timeout, allows last dead members to start back up
	for {
		select {
		case <-timeout:
			return nil, fmt.Errorf("Timed out waiting for services from %s", strings.Join(members, ", "))
		default:
			// request services from each member until successful
			for _, member := range members {
				// memberTimeout is how long to wait for a member to respond with list of services
				memberTimeout := time.After(3 * time.Second)

				// ask a member for its services
				config.Log.Trace("[cluster] - Attempting to request services from %s...", member)
				_, err := conn.Do("PUBLISH", "portal", fmt.Sprintf("get-services %s", member))
				if err != nil {
					return nil, err
				}

				// wait for member to respond
				for {
					select {
					case <-memberTimeout:
						config.Log.Debug("[cluster] - Timed out waiting for services from %s", member)
						goto nextMember
					case msg := <-message:
						switch v := msg.(type) {
						case redis.Message:
							config.Log.Trace("[cluster] - Received message on 'services' channel")
							services, err := marshalSvcs(v.Data)
							if err != nil {
								return nil, fmt.Errorf("Failed to marshal services - %s", err)
							}
							config.Log.Trace("[cluster] - Services from cluster: %#v\n", *services)
							return *services, nil
						case error:
							return nil, fmt.Errorf("Subscriber failed to receive services - %s", v.Error())
						}
					}
				}
			nextMember:
			}
		}
	}
}

// GetServer - likely will never be called
func (r *Redis) GetServer(svcId, srvId string) (*core.Server, error) {
	return common.GetServer(svcId, srvId)
}

// GetRoutes gets a list of routes from the database, or another cluster member.
func (r *Redis) GetRoutes() ([]core.Route, error) {
	if database.CentralStore {
		return database.GetRoutes()
	}

	conn := pool.Get()
	defer conn.Close()

	// get known members(other than me) to 'poll' for routes
	members, _ := redis.Strings(conn.Do("SMEMBERS", "members"))
	if len(members) == 0 {
		// should only happen on new cluster
		// assume i'm ok to be primary so don't reset imported routes
		config.Log.Trace("[cluster] - Assuming OK to be primary, using routes from my database...")
		return common.GetRoutes()
	}
	for i := range members {
		if members[i] == self {
			// if i'm in the list of members, new requests should have failed while `waitForMembers`ing
			config.Log.Trace("[cluster] - Assuming I was in sync, using routes from my database...")
			return common.GetRoutes()
		}
	}

	c, err := redis.DialURL(config.ClusterConnection, redis.DialConnectTimeout(15*time.Second), redis.DialPassword(config.ClusterToken))
	if err != nil {
		return nil, fmt.Errorf("Failed to reach redis for routes subscriber - %s", err)
	}
	defer c.Close()

	message := make(chan interface{})
	subconn := redis.PubSubConn{c}

	// subscribe to channel that routes will be published on
	if err := subconn.Subscribe("routes"); err != nil {
		return nil, fmt.Errorf("Failed to reach redis for routes subscriber - %s", err)
	}
	defer subconn.Close()

	// listen always
	go func() {
		for {
			message <- subconn.Receive()
		}
	}()

	// todo: maybe use ttl?
	// timeout is how long to wait for the listed members to come back online
	timeout := time.After(time.Duration(20) * time.Second)

	// loop attempts for timeout, allows last dead members to start back up
	for {
		select {
		case <-timeout:
			return nil, fmt.Errorf("Timed out waiting for routes from %s", strings.Join(members, ", "))
		default:
			// request routes from each member until successful
			for _, member := range members {
				// memberTimeout is how long to wait for a member to respond with list of routes
				memberTimeout := time.After(3 * time.Second)

				// ask a member for its routes
				config.Log.Trace("[cluster] - Attempting to request routes from %s...", member)
				_, err := conn.Do("PUBLISH", "portal", fmt.Sprintf("get-routes %s", member))
				if err != nil {
					return nil, err
				}

				// wait for member to respond
				for {
					select {
					case <-memberTimeout:
						config.Log.Debug("[cluster] - Timed out waiting for routes from %s", member)
						goto nextRouteMember
					case msg := <-message:
						switch v := msg.(type) {
						case redis.Message:
							config.Log.Trace("[cluster] - Received message on 'routes' channel")
							var routes []core.Route
							err = parseBody(v.Data, &routes)
							if err != nil {
								return nil, fmt.Errorf("Failed to marshal routes - %s", err)
							}
							config.Log.Trace("[cluster] - Routes from cluster: %#v\n", routes)
							return routes, nil
						case error:
							return nil, fmt.Errorf("Subscriber failed to receive routes - %s", v.Error())
						}
					}
				}
			nextRouteMember:
			}
		}
	}
}

// GetCerts gets a list of certs from the database, or another cluster member.
func (r *Redis) GetCerts() ([]core.CertBundle, error) {
	if database.CentralStore {
		return database.GetCerts()
	}

	conn := pool.Get()
	defer conn.Close()

	// get known members(other than me) to 'poll' for certs
	members, _ := redis.Strings(conn.Do("SMEMBERS", "members"))
	if len(members) == 0 {
		// should only happen on new cluster
		// assume i'm ok to be primary so don't reset imported certs
		config.Log.Trace("[cluster] - Assuming OK to be primary, using certs from my database...")
		return common.GetCerts()
	}
	for i := range members {
		if members[i] == self {
			// if i'm in the list of members, new requests should have failed while `waitForMembers`ing
			config.Log.Trace("[cluster] - Assuming I was in sync, using certs from my database...")
			return common.GetCerts()
		}
	}

	c, err := redis.DialURL(config.ClusterConnection, redis.DialConnectTimeout(15*time.Second), redis.DialPassword(config.ClusterToken))
	if err != nil {
		return nil, fmt.Errorf("Failed to reach redis for certs subscriber - %s", err)
	}
	defer c.Close()

	message := make(chan interface{})
	subconn := redis.PubSubConn{c}

	// subscribe to channel that certs will be published on
	if err := subconn.Subscribe("certs"); err != nil {
		return nil, fmt.Errorf("Failed to reach redis for certs subscriber - %s", err)
	}
	defer subconn.Close()

	// listen always
	go func() {
		for {
			message <- subconn.Receive()
		}
	}()

	// todo: maybe use ttl?
	// timeout is how long to wait for the listed members to come back online
	timeout := time.After(time.Duration(20) * time.Second)

	// loop attempts for timeout, allows last dead members to start back up
	for {
		select {
		case <-timeout:
			return nil, fmt.Errorf("Timed out waiting for certs from %s", strings.Join(members, ", "))
		default:
			// request certs from each member until successful
			for _, member := range members {
				// memberTimeout is how long to wait for a member to respond with list of certs
				memberTimeout := time.After(3 * time.Second)

				// ask a member for its certs
				config.Log.Trace("[cluster] - Attempting to request certs from %s...", member)
				_, err := conn.Do("PUBLISH", "portal", fmt.Sprintf("get-certs %s", member))
				if err != nil {
					return nil, err
				}

				// wait for member to respond
				for {
					select {
					case <-memberTimeout:
						config.Log.Debug("[cluster] - Timed out waiting for certs from %s", member)
						goto nextCertMember
					case msg := <-message:
						switch v := msg.(type) {
						case redis.Message:
							config.Log.Trace("[cluster] - Received message on 'certs' channel")
							var certs []core.CertBundle
							err = parseBody(v.Data, &certs)
							if err != nil {
								return nil, fmt.Errorf("Failed to marshal certs - %s", err)
							}
							config.Log.Trace("[cluster] - Certs from cluster: %#v\n", certs)
							return certs, nil
						case error:
							return nil, fmt.Errorf("Subscriber failed to receive certs - %s", v.Error())
						}
					}
				}
			nextCertMember:
			}
		}
	}
}

////////////////////////////////////////////////////////////////////////////////
// PRIVATE
////////////////////////////////////////////////////////////////////////////////

// cleanup cleans up members not present after ttl seconds
func (r Redis) cleanup() {
	// cycle every second to check for dead members
	tick := time.Tick(time.Second)
	conn := pool.Get()
	defer conn.Close()

	for _ = range tick {
		// get list of members that should be alive
		members, err := redis.Strings(conn.Do("SMEMBERS", "members"))
		if err != nil {
			config.Log.Error("[cluster] - Failed to reach redis for cleanup - %s", err)
			// clear balancer rules ("stop balancing if we are 'dead'")
			balance.SetServices(make([]core.Service, 0, 0))
			os.Exit(1)
		}
		for _, member := range members {
			// if the member timed out, remove the member from the member set
			exist, _ := redis.Int(conn.Do("EXISTS", member))
			if exist == 0 {
				conn.Do("SREM", "members", member)
				config.Log.Info("[cluster] - Member '%s' assumed dead. Removed.", member)
			}
		}
	}
	conn.Close()
}

// heartbeat records that the member is still alive
func (r Redis) heartbeat() {
	tick := time.Tick(beat)
	// write timeout set in connection pool so each 'beat' ensures we can talk to redis (network partition) (rather than create new connection)
	conn := pool.Get()
	defer conn.Close()

	for _ = range tick {
		config.Log.Trace("[cluster] - Heartbeat...")
		_, err := conn.Do("SET", self, "alive", "EX", ttl)
		if err != nil {
			conn.Close()
			config.Log.Error("[cluster] - Failed to heartbeat - %s", err)
			// clear balancer rules ("stop balancing if we are 'dead'")
			balance.SetServices(make([]core.Service, 0, 0))
			os.Exit(1)
		}
		// re-add ourself to member list (just in case)
		_, err = conn.Do("SADD", "members", self)
		if err != nil {
			conn.Close()
			config.Log.Error("[cluster] - Failed to add myself to list of members - %s", err)
			// clear balancer rules ("stop balancing if we are 'dead'")
			balance.SetServices(make([]core.Service, 0, 0))
			os.Exit(1)
		}
	}
}

// creates a redis connection pool to use
func (r Redis) newPool(server, password string) *redis.Pool {
	return &redis.Pool{
		MaxIdle:     3,
		IdleTimeout: 5 * time.Second,
		Dial: func() (redis.Conn, error) {
			c, err := redis.DialURL(server, redis.DialConnectTimeout(30*time.Second),
				redis.DialWriteTimeout(10*time.Second), redis.DialPassword(password))

			if err != nil {
				return nil, fmt.Errorf("Failed to reach redis - %s", err)
			}
			return c, err
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			_, err := c.Do("PING")
			return err
		},
	}
}

// subscribe listens on the portal channel and acts based on messages received
func (r Redis) subscribe() {
	config.Log.Info("[cluster] - Redis subscribing on %s...", config.ClusterConnection)

	// listen for published messages
	for {
		switch v := r.subconn.Receive().(type) {
		case redis.Message:
			switch pdata := strings.FieldsFunc(string(v.Data), keepSubstrings); pdata[0] {
			// SERVICES ///////////////////////////////////////////////////////////////////////////////////////////////
			case "get-services":
				if len(pdata) != 2 {
					config.Log.Error("[cluster] - member not passed in message")
					break
				}
				member := pdata[1]

				if member == self {
					svcs, err := common.GetServices()
					if err != nil {
						config.Log.Error("[cluster] - Failed to get services - %s", err)
						break
					}
					services, err := json.Marshal(svcs)
					if err != nil {
						config.Log.Error("[cluster] - Failed to marshal services - %s", err)
						break
					}
					config.Log.Debug("[cluster] - get-services requested, publishing my services")
					conn := pool.Get()
					conn.Do("PUBLISH", "services", fmt.Sprintf("%s", services))
					conn.Close()
				}
			case "set-services":
				if len(pdata) != 2 {
					config.Log.Error("[cluster] - services not passed in message")
					break
				}
				services, err := marshalSvcs([]byte(pdata[1]))
				if err != nil {
					config.Log.Error("[cluster] - Failed to marshal services - %s", err)
					break
				}
				err = common.SetServices(*services)
				if err != nil {
					config.Log.Error("[cluster] - Failed to set services - %s", err)
					break
				}
				actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-services %s", *services))))
				config.Log.Trace("[cluster] - set-services hash - %s", actionHash)
				conn := pool.Get()
				conn.Do("SADD", actionHash, self)
				conn.Close()
				config.Log.Debug("[cluster] - set-services successful")
			case "set-service":
				if len(pdata) != 2 {
					// shouldn't happen unless redis is not secure and someone manually `publishJson`es
					config.Log.Error("[cluster] - service not passed in message")
					break
				}
				svc, err := marshalSvc([]byte(pdata[1]))
				if err != nil {
					config.Log.Error("[cluster] - Failed to marshal service - %s", err)
					break
				}
				err = common.SetService(svc)
				if err != nil {
					config.Log.Error("[cluster] - Failed to set service - %s", err)
					break
				}
				actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-service %s", *svc))))
				config.Log.Trace("[cluster] - set-service hash - %s", actionHash)
				conn := pool.Get()
				conn.Do("SADD", actionHash, self)
				conn.Close()
				config.Log.Debug("[cluster] - set-service successful")
			case "delete-service":
				if len(pdata) != 2 {
					config.Log.Error("[cluster] - service id not passed in message")
					break
				}
				svcId := pdata[1]
				err := common.DeleteService(svcId)
				if err != nil {
					config.Log.Error("[cluster] - Failed to delete service - %s", err)
					break
				}
				actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("delete-service %s", svcId))))
				config.Log.Trace("[cluster] - delete-service hash - %s", actionHash)
				conn := pool.Get()
				conn.Do("SADD", actionHash, self)
				conn.Close()
				config.Log.Debug("[cluster] - delete-service successful")
			// SERVERS ///////////////////////////////////////////////////////////////////////////////////////////////
			case "set-servers":
				if len(pdata) != 3 {
					config.Log.Error("[cluster] - service id not passed in message")
					break
				}
				svcId := pdata[2]
				servers, err := marshalSrvs([]byte(pdata[1]))
				if err != nil {
					config.Log.Error("[cluster] - Failed to marshal server - %s", err)
					break
				}
				err = common.SetServers(svcId, *servers)
				if err != nil {
					config.Log.Error("[cluster] - Failed to set servers - %s", err)
					break
				}
				actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-servers %s %s", *servers, svcId))))
				config.Log.Trace("[cluster] - set-servers hash - %s", actionHash)
				conn := pool.Get()
				conn.Do("SADD", actionHash, self)
				conn.Close()
				config.Log.Debug("[cluster] - set-servers successful")
			case "set-server":
				if len(pdata) != 3 {
					// shouldn't happen unless redis is not secure and someone manually publishJson
					config.Log.Error("[cluster] - service id not passed in message")
					break
				}
				svcId := pdata[2]
				server, err := marshalSrv([]byte(pdata[1]))
				if err != nil {
					config.Log.Error("[cluster] - Failed to marshal server - %s", err)
					break
				}
				err = common.SetServer(svcId, server)
				if err != nil {
					config.Log.Error("[cluster] - Failed to set server - %s", err)
					break
				}
				actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-server %s %s", *server, svcId))))
				config.Log.Trace("[cluster] - set-server hash - %s", actionHash)
				conn := pool.Get()
				conn.Do("SADD", actionHash, self)
				conn.Close()
				config.Log.Debug("[cluster] - set-server successful")
			case "delete-server":
				if len(pdata) != 3 {
					config.Log.Error("[cluster] - service id not passed in message")
					break
				}
				srvId := pdata[1]
				svcId := pdata[2]
				err := common.DeleteServer(svcId, srvId)
				if err != nil {
					config.Log.Error("[cluster] - Failed to delete server - %s", err)
					break
				}
				actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("delete-server %s %s", srvId, svcId))))
				config.Log.Trace("[cluster] - delete-server hash - %s", actionHash)
				conn := pool.Get()
				conn.Do("SADD", actionHash, self)
				conn.Close()
				config.Log.Debug("[cluster] - delete-server successful")
			// ROUTES ///////////////////////////////////////////////////////////////////////////////////////////////
			case "get-routes":

				if len(pdata) != 2 {
					config.Log.Error("[cluster] - member not passed in message")
					break
				}
				member := pdata[1]

				if member == self {
					rts, err := common.GetRoutes()
					if err != nil {
						config.Log.Error("[cluster] - Failed to get routes - %s", err)
						break
					}
					routes, err := json.Marshal(rts)
					if err != nil {
						config.Log.Error("[cluster] - Failed to marshal routes - %s", err)
						break
					}
					config.Log.Debug("[cluster] - get-routes requested, publishing my routes")
					conn := pool.Get()
					conn.Do("PUBLISH", "routes", fmt.Sprintf("%s", routes))
					conn.Close()
				}
			case "set-routes":
				if len(pdata) != 2 {
					config.Log.Error("[cluster] - routes not passed in message")
					break
				}

				var routes []core.Route
				err := parseBody([]byte(pdata[1]), &routes)
				if err != nil {
					config.Log.Error("[cluster] - Failed to marshal routes - %s", err)
					break
				}
				err = common.SetRoutes(routes)
				if err != nil {
					config.Log.Error("[cluster] - Failed to set routes - %s", err)
					break
				}
				actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-routes %s", routes))))
				config.Log.Trace("[cluster] - set-routes hash - %s", actionHash)
				conn := pool.Get()
				conn.Do("SADD", actionHash, self)
				conn.Close()
				config.Log.Debug("[cluster] - set-routes successful")
			case "set-route":
				if len(pdata) != 2 {
					// shouldn't happen unless redis is not secure and someone manually `publishJson`es
					config.Log.Error("[cluster] - route not passed in message")
					break
				}
				var rte core.Route
				err := parseBody([]byte(pdata[1]), &rte)
				if err != nil {
					config.Log.Error("[cluster] - Failed to marshal route - %s", err)
					break
				}
				err = common.SetRoute(rte)
				if err != nil {
					config.Log.Error("[cluster] - Failed to set route - %s", err)
					break
				}
				actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-route %s", rte))))
				config.Log.Trace("[cluster] - set-route hash - %s", actionHash)
				conn := pool.Get()
				conn.Do("SADD", actionHash, self)
				conn.Close()
				config.Log.Debug("[cluster] - set-route successful")
			case "delete-route":
				if len(pdata) != 2 {
					config.Log.Error("[cluster] - route not passed in message")
					break
				}
				var rte core.Route
				err := parseBody([]byte(pdata[1]), &rte)
				if err != nil {
					config.Log.Error("[cluster] - Failed to marshal route - %s", err)
					break
				}
				err = common.DeleteRoute(rte)
				if err != nil {
					config.Log.Error("[cluster] - Failed to delete route - %s", err)
					break
				}
				actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("delete-route %s", rte))))
				config.Log.Trace("[cluster] - delete-route hash - %s", actionHash)
				conn := pool.Get()
				conn.Do("SADD", actionHash, self)
				conn.Close()
				config.Log.Debug("[cluster] - delete-route successful")
			// CERTS ///////////////////////////////////////////////////////////////////////////////////////////////
			case "get-certs":
				if len(pdata) != 2 {
					config.Log.Error("[cluster] - member not passed in message")
					break
				}

				member := pdata[1]

				if member == self {
					rts, err := common.GetCerts()
					if err != nil {
						config.Log.Error("[cluster] - Failed to get certs - %s", err)
						break
					}
					certs, err := json.Marshal(rts)
					if err != nil {
						config.Log.Error("[cluster] - Failed to marshal certs - %s", err)
						break
					}
					config.Log.Debug("[cluster] - get-certs requested, publishing my certs")
					conn := pool.Get()
					conn.Do("PUBLISH", "certs", fmt.Sprintf("%s", certs))
					conn.Close()
				}
			case "set-certs":
				if len(pdata) != 2 {
					config.Log.Error("[cluster] - certs not passed in message")
					break
				}
				var certs []core.CertBundle
				err := parseBody([]byte(pdata[1]), &certs)
				if err != nil {
					config.Log.Error("[cluster] - Failed to marshal certs - %s", err)
					break
				}
				err = common.SetCerts(certs)
				if err != nil {
					config.Log.Error("[cluster] - Failed to set certs - %s", err)
					break
				}
				actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-certs %s", certs))))
				config.Log.Trace("[cluster] - set-certs hash - %s", actionHash)
				conn := pool.Get()
				conn.Do("SADD", actionHash, self)
				conn.Close()
				config.Log.Debug("[cluster] - set-certs successful")
			case "set-cert":
				if len(pdata) != 2 {
					// shouldn't happen unless redis is not secure and someone manually `publishJson`es
					config.Log.Error("[cluster] - cert not passed in message")
					break
				}
				var crt core.CertBundle
				err := parseBody([]byte(pdata[1]), &crt)
				if err != nil {
					config.Log.Error("[cluster] - Failed to marshal cert - %s", err)
					break
				}
				err = common.SetCert(crt)
				if err != nil {
					config.Log.Error("[cluster] - Failed to set cert - %s", err)
					break
				}
				actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("set-cert %s", crt))))
				config.Log.Trace("[cluster] - set-cert hash - %s", actionHash)
				conn := pool.Get()
				conn.Do("SADD", actionHash, self)
				conn.Close()
				config.Log.Debug("[cluster] - set-cert successful")
			case "delete-cert":
				if len(pdata) != 2 {
					config.Log.Error("[cluster] - cert id not passed in message")
					break
				}
				var crt core.CertBundle
				err := parseBody([]byte(pdata[1]), &crt)
				if err != nil {
					config.Log.Error("[cluster] - Failed to marshal cert - %s", err)
					break
				}
				err = common.DeleteCert(crt)
				if err != nil {
					config.Log.Error("[cluster] - Failed to delete cert - %s", err)
					break
				}
				actionHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("delete-cert %s", crt))))
				config.Log.Trace("[cluster] - delete-cert hash - %s", actionHash)
				conn := pool.Get()
				conn.Do("SADD", actionHash, self)
				conn.Close()
				config.Log.Debug("[cluster] - delete-cert successful")
			default:
				config.Log.Error("[cluster] - Recieved unknown data on %s: %s", v.Channel, string(v.Data))
			}
		case error:
			config.Log.Error("[cluster] - Subscriber failed to receive - %s", v.Error())
			if strings.Contains(v.Error(), "closed network connection") {
				// clear balancer rules ("stop balancing if we are 'dead'")
				balance.SetServices(make([]core.Service, 0, 0))
				// exit so we don't get spammed with logs
				os.Exit(1)
			}
			continue
		}
	}
}

// publishJson publishes service[s] to the "portal" channel
func (r Redis) publishJson(conn redis.Conn, action string, v interface{}) error {
	s, err := json.Marshal(v)
	if err != nil {
		return BadJson
	}

	// todo: should create new connection(or use pool) - single connection limits concurrency
	_, err = conn.Do("PUBLISH", "portal", fmt.Sprintf("%s %s", action, s))
	return err
}

// publishStringJson publishes server[s] to the "portal" channel
func (r Redis) publishStringJson(conn redis.Conn, action, svcId string, v interface{}) error {
	s, err := json.Marshal(v)
	if err != nil {
		return BadJson
	}

	// todo: should create new connection(or use pool) - single connection limits concurrency
	_, err = conn.Do("PUBLISH", "portal", fmt.Sprintf("%s %s %s", action, s, svcId))
	return err
}

// publishString publishes string[s] to the "portal" channel
func (r Redis) publishString(conn redis.Conn, action string, s ...string) error {
	// todo: should create new connection(or use pool) - single connection limits concurrency
	_, err := conn.Do("PUBLISH", "portal", fmt.Sprintf("%s %s", action, strings.Join(s, " ")))
	return err
}

// waitForMembers waits for all members to apply the action
func (r Redis) waitForMembers(conn redis.Conn, actionHash string) error {
	config.Log.Trace("[cluster] - Waiting for updates to %s", actionHash)

	// clear cruft
	defer conn.Do("DEL", actionHash)

	// todo: make timeout configurable
	// timeout is the amount of time to wait for members to apply the action
	timeout := time.After(30 * time.Second)
	tick := time.Tick(500 * time.Millisecond)
	var list []string
	var err error
	for {
		select {
		case <-tick:
			// compare who we know about, to who performed the update
			list, err = redis.Strings(conn.Do("SDIFF", "members", actionHash))
			if err != nil {
				return err
			}
			if len(list) == 0 {
				// if all members respond, all is well
				return nil
			}
		// if members don't respond in time, return error
		case <-timeout:
			return fmt.Errorf("Member(s) '%s' failed to set-service", list)
		}
	}
}

// used in keepSubstrings
var lastQuote = rune(0)

// because subscribe needs to split on spaces, but pages can contain spaces, we
// need to use a custom FieldsFunc. *Thanks Soheil
func keepSubstrings(c rune) bool {
	switch {
	case c == lastQuote:
		lastQuote = rune(0)
		return false
	case lastQuote != rune(0):
		return false
	case unicode.In(c, unicode.Quotation_Mark):
		lastQuote = c
		return false
	default:
		return unicode.IsSpace(c)
	}
}
