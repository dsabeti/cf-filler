package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/square/certstrap/pkix"

	yaml "gopkg.in/yaml.v2"
)

const (
	CfgNone             = 0
	CfgWithSubdomainURI = 1 << iota
	CfgWithHTTPSURL
)

type OutputData map[string]string

func (o OutputData) AddSystemComponent(name string, cfgFlags int) {
	sysDomain := o["system_domain"]
	uri := fmt.Sprintf("%s.%s", name, sysDomain)
	o[fmt.Sprintf("%s_uri", name)] = uri

	if cfgFlags&CfgWithSubdomainURI != 0 {
		o[fmt.Sprintf("%s_subdomain_uri", name)] = fmt.Sprintf("*.%s", uri)
	}
	if cfgFlags&CfgWithHTTPSURL != 0 {
		o[fmt.Sprintf("%s_url", name)] = fmt.Sprintf("https://%s", uri)
	}
}

func init() {
	rand.Seed(time.Now().Unix())
}

func generatePassword() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic("unable to read rand bytes: " + err.Error())
	}
	return strings.Trim(base64.RawURLEncoding.EncodeToString(bytes), "-_")
}

func (o OutputData) GeneratePasswords(keynames ...string) {
	for _, name := range keynames {
		o[name] = generatePassword()
	}
}

func mainWithError() error {
	var dnsBaseName string
	flag.StringVar(&dnsBaseName, "dns-base", "example.com", "DNS base name, e.g. example.com")

	var envName string
	flag.StringVar(&envName, "env", "myenv", "Short name for environment, e.g. myenv")

	flag.Parse()

	dnsName := fmt.Sprintf("%s.%s", envName, dnsBaseName)

	o := OutputData{}
	system_domain := dnsName
	o["system_domain"] = system_domain
	o["app_domain"] = dnsName
	o.AddSystemComponent("uaa", CfgWithSubdomainURI|CfgWithHTTPSURL)
	o["uaa_token_url"] = fmt.Sprintf("https://%s/oauth/token", o["uaa_uri"])

	o.AddSystemComponent("login", CfgWithSubdomainURI)
	o.AddSystemComponent("api", CfgWithHTTPSURL)
	o.AddSystemComponent("loggregator", CfgNone)
	o.AddSystemComponent("doppler", CfgWithSubdomainURI)
	o.AddSystemComponent("blobstore", CfgNone)
	o["blobstore_public_url"] = fmt.Sprintf("http://%s", o["blobstore_uri"])
	o["blobstore_private_url"] = "https://blobstore.service.cf.internal:4443"
	o["metron_agent_deployment_name"] = fmt.Sprintf("%s-cf", envName)

	o.GeneratePasswords(
		"blobstore_admin_users_password",
		"blobstore_secure_link_secret",
		"cc_bulk_api_password",
		"cc_db_encryption_key",
		"cc_internal_api_password",
		"cc_staging_upload_password",
		"cf_mysql_mysql_admin_password",
		"cf_mysql_mysql_cluster_health_password",
		"cf_mysql_mysql_galera_healthcheck_endpoint_password",
		"cf_mysql_mysql_galera_healthcheck_password",
		"cf_mysql_mysql_roadmin_password",
		"cf_mysql_mysql_seeded_databases_cc_password",
		"cf_mysql_mysql_seeded_databases_diego_password",
		"cf_mysql_mysql_seeded_databases_uaa_password",
		"nats_password",
		"router_status_password",
		"uaa_scim_users_admin_password",
		"dropsonde_shared_secret",
		"router_route_services_secret",
		"uaa_admin_client_secret",
		"uaa_clients_cc-routing_secret",
		"uaa_clients_cc-service-dashboards_secret",
		"uaa_clients_cloud_controller_username_lookup_secret",
		"uaa_clients_doppler_secret",
		"uaa_clients_gorouter_secret",
		"uaa_clients_ssh-proxy_secret",
		"uaa_clients_tcp_emitter_secret",
		"uaa_clients_tcp_router_secret",
		"uaa_login_client_secret",
		"consul_encrypt_keys",
		"diego_bbs_encryption_keys_passphrase",
	)

	o["uaa_scim_users_admin_name"] = "admin"
	o["blobstore_admin_users_username"] = "blobstore-user"
	o["cc_staging_upload_user"] = "staging_user"
	o["cf_mysql_mysql_galera_healthcheck_endpoint_username"] = "galera_healthcheck"
	o["cf_mysql_mysql_seeded_databases_cc_username"] = "cloud_controller"
	o["cf_mysql_mysql_seeded_databases_diego_username"] = "diego"
	o["cf_mysql_mysql_seeded_databases_uaa_username"] = "uaa"
	o["nats_user"] = "nats"
	o["router_status_user"] = "router-status"

	o.GenerateCerts(
		&CA{
			VarName_CA: "etcd_ca_cert",
			CommonName: "etcdCA",
		},
		&CertKeyPair{
			VarName_Cert: "etcd_server_cert",
			VarName_Key:  "etcd_server_key",
			CommonName:   "etcd.service.cf.internal",
			Domains: []string{
				"*.etcd.service.cf.internal",
				"etcd.service.cf.internal",
			},
		},
		&CertKeyPair{
			VarName_Cert: "etcd_client_cert",
			VarName_Key:  "etcd_client_key",
			CommonName:   "clientName",
		},
	)
	o.GenerateCerts(
		&CA{
			VarName_CA: "etcd_peer_ca_cert",
			CommonName: "peerCA",
		},
		&CertKeyPair{
			VarName_Cert: "etcd_peer_cert",
			VarName_Key:  "etcd_peer_key",
			CommonName:   "etcd.service.cf.internal",
			Domains: []string{
				"*.etcd.service.cf.internal",
				"etcd.service.cf.internal",
			},
		},
	)

	outBytes, err := yaml.Marshal(o)
	if err != nil {
		return fmt.Errorf("marshaling output as yaml: %s", err)
	}
	os.Stdout.Write(outBytes)

	return nil
}

func main() {
	if err := mainWithError(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

type CA struct {
	VarName_CA string
	CommonName string

	key  *pkix.Key
	cert *pkix.Certificate
}

const KeyBits = 2048
const CAExpiryYears = 10
const HostCertExpiryYears = 2

func (ca *CA) Init() error {
	var err error
	ca.key, err = pkix.CreateRSAKey(KeyBits)
	if err != nil {
		return fmt.Errorf("create ca key: %s", err)
	}

	ca.cert, err = pkix.CreateCertificateAuthority(ca.key, "", CAExpiryYears,
		"", "", "", "", ca.CommonName)
	if err != nil {
		return fmt.Errorf("create ca cert: %s", err)
	}

	return nil
}

func (ca *CA) CACertAsString() (string, error) {
	return asString(ca.cert)
}

func (ca *CA) InitCertKeyPair(certKeyPair *CertKeyPair) error {
	var err error
	certKeyPair.key, err = pkix.CreateRSAKey(KeyBits)
	if err != nil {
		return fmt.Errorf("create host key: %s", err)
	}
	csr, err := pkix.CreateCertificateSigningRequest(certKeyPair.key, "", nil,
		certKeyPair.Domains, "", "", "", "", certKeyPair.CommonName)
	if err != nil {
		return fmt.Errorf("create host csr: %s", err)
	}

	certKeyPair.cert, err = pkix.CreateCertificateHost(ca.cert, ca.key, csr, HostCertExpiryYears)
	if err != nil {
		return fmt.Errorf("sign host csr: %s", err)
	}

	return nil
}

type CertKeyPair struct {
	VarName_Cert string
	VarName_Key  string
	CommonName   string
	Domains      []string

	key  *pkix.Key
	cert *pkix.Certificate
}

type exportable interface {
	Export() ([]byte, error)
}

func asString(e exportable) (string, error) {
	pemBytes, err := e.Export()
	if err != nil {
		return "", fmt.Errorf("export pem: %s", err)
	}

	return string(pemBytes), nil
}

func (kp *CertKeyPair) PrivateKeyAsString() (string, error) {
	pemBytes, err := kp.key.ExportPrivate()
	if err != nil {
		return "", fmt.Errorf("export private key: %s", err)
	}

	return string(pemBytes), nil
}

func (kp *CertKeyPair) CertAsString() (string, error) {
	return asString(kp.cert)
}

func (o OutputData) GenerateCerts(ca *CA, certKeyPairs ...*CertKeyPair) {
	err := ca.Init()
	if err != nil {
		panic(err)
	}
	o[ca.VarName_CA], err = ca.CACertAsString()
	if err != nil {
		panic(err)
	}
	for _, certKeyPair := range certKeyPairs {
		err = ca.InitCertKeyPair(certKeyPair)
		if err != nil {
			panic(err)
		}
		o[certKeyPair.VarName_Cert], err = certKeyPair.CertAsString()
		if err != nil {
			panic(err)
		}
		o[certKeyPair.VarName_Key], err = certKeyPair.PrivateKeyAsString()
		if err != nil {
			panic(err)
		}
	}
}

/* TODO: add these

blobstore_tls_ca_cert
	blobstore_tls_cert
	blobstore_tls_private_key

consul_agent_ca_cert
	consul_agent_agent_key
	consul_agent_cert
	consul_agent_server_cert
	consul_agent_server_key

diego_bbs_ca_cert
	diego_bbs_client_cert
	diego_bbs_client_key
	diego_bbs_server_cert
	diego_bbs_server_key

diego_bbs_sql_db_connection_string

diego_ssh_proxy_host_key
diego_ssh_proxy_host_key_fingerprint

loggregator_tls_ca_cert
	doppler_tls_server_cert
	doppler_tls_server_key
	metron_metron_agent_tls_client_cert
	metron_metron_agent_tls_client_key


uaa_jwt_signing_key
uaa_jwt_verification_key

uaa_sslCertificate
uaa_sslPrivateKey

*/
