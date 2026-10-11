package publish

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

const netlifyDefaultBaseURL = "https://api.netlify.com/api/v1"

// Suivi du déploiement : variables (et non constantes) pour que les tests
// les ramènent à la milliseconde.
var (
	netlifyPollInterval = 2 * time.Second
	netlifyPollTimeout  = 90 * time.Second
)

// Netlify : déploiement par archive ZIP (méthode documentée de l'API).
type Netlify struct {
	Token   string
	BaseURL string       // défaut "https://api.netlify.com/api/v1" (surchargé en test)
	HTTP    *http.Client // défaut : client avec timeout 60 s
}

// netlifySite : réponse de POST /sites avec un corps zip — l'objet site,
// complété du deploy_id du déploiement ainsi lancé.
type netlifySite struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	SSLURL   string `json:"ssl_url"`
	AdminURL string `json:"admin_url"`
	DeployID string `json:"deploy_id"`
}

// netlifyDeploy : objet deploy (POST /sites/{site}/deploys, GET /deploys/{id}).
// url/ssl_url y désignent le site, deploy_ssl_url ce déploiement précis.
type netlifyDeploy struct {
	ID           string `json:"id"`
	SiteID       string `json:"site_id"`
	URL          string `json:"url"`
	SSLURL       string `json:"ssl_url"`
	DeploySSLURL string `json:"deploy_ssl_url"`
	AdminURL     string `json:"admin_url"`
	State        string `json:"state"`
	ErrorMessage string `json:"error_message"`
}

// netlifySiteRe : site_id (UUID) ou domaine. Le motif interdit « / » et un
// point initial : la valeur est insérée dans le chemin de l'URL.
var netlifySiteRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)

// Deploy : site == "" ⇒ crée un nouveau site ; sinon site est un site_id ou un
// nom de domaine netlify (xxx.netlify.app) existant à redéployer.
//
// Après l'envoi du zip, l'état du déploiement est suivi jusqu'à « ready ».
// Deux cas particuliers, voulus :
//   - si le suivi dépasse son plafond (~90 s) alors que Netlify traite
//     toujours l'archive, Deploy renvoie un SUCCÈS avec l'URL : le zip a été
//     accepté et le déploiement se poursuit côté Netlify, mais sa mise en
//     ligne n'a pas été constatée. Un contexte annulé ou expiré, lui, est
//     une erreur ;
//   - en cas d'échec après l'envoi (état « error », contexte annulé), le
//     Result partiel est renvoyé AVEC l'erreur : Result.Site permet de
//     redéployer sur le site déjà créé au lieu d'en créer un second.
func (n *Netlify) Deploy(ctx context.Context, site string, files []File) (Result, error) {
	if err := ValidateFiles(files); err != nil {
		return Result{}, err
	}
	token, err := cleanToken(n.Token, "Netlify")
	if err != nil {
		return Result{}, err
	}
	if site, err = normalizeNetlifySite(site); err != nil {
		return Result{}, err
	}
	archive, err := buildZip(files)
	if err != nil {
		return Result{}, err
	}

	res := Result{Provider: "netlify"}
	if site == "" {
		var s netlifySite
		status, err := n.call(ctx, token, http.MethodPost, "/sites", "application/zip", archive, &s)
		if err != nil {
			return Result{}, err
		}
		if s.ID == "" {
			return Result{}, upstreamError("netlify", status, "réponse inattendue : site sans identifiant")
		}
		res.Site, res.DeployID, res.AdminURL = s.ID, s.DeployID, s.AdminURL
		res.URL = cmp.Or(s.SSLURL, s.URL)
		if res.URL == "" && s.Name != "" {
			res.URL = "https://" + s.Name + ".netlify.app"
		}
	} else {
		var d netlifyDeploy
		status, err := n.call(ctx, token, http.MethodPost, "/sites/"+url.PathEscape(site)+"/deploys", "application/zip", archive, &d)
		if err != nil {
			return Result{}, err
		}
		if d.ID == "" {
			return Result{}, upstreamError("netlify", status, "réponse inattendue : déploiement sans identifiant")
		}
		res.Site, res.DeployID, res.AdminURL = cmp.Or(d.SiteID, site), d.ID, d.AdminURL
		res.URL = cmp.Or(d.SSLURL, d.URL, d.DeploySSLURL)
	}

	// Sans deploy_id il n'y a rien à suivre : le site existe et le zip a été
	// accepté ; échouer ici pousserait l'utilisateur à créer un doublon.
	if res.DeployID == "" {
		return res, nil
	}
	return res, n.waitReady(ctx, token, res.DeployID)
}

// waitReady : nil si « ready » OU si le plafond de suivi est atteint sans
// verdict (voir Deploy). Une erreur passagère pendant le suivi (5xx, 429,
// réseau) ne condamne pas un déploiement qui se déroule peut-être très bien :
// on réessaie au tour suivant.
func (n *Netlify) waitReady(ctx context.Context, token, deployID string) error {
	deadline := time.Now().Add(netlifyPollTimeout)
	timer := time.NewTimer(0) // premier sondage immédiat : un petit site est souvent prêt tout de suite
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("netlify : %w", ctx.Err())
		case <-timer.C:
		}
		var d netlifyDeploy
		status, err := n.call(ctx, token, http.MethodGet, "/deploys/"+url.PathEscape(deployID), "", nil, &d)
		switch {
		case err != nil:
			var pe *Error
			if !errors.As(err, &pe) || (pe.Code != CodeUpstream && pe.Code != CodeRateLimited) {
				return err
			}
		case d.State == "ready":
			return nil
		case d.State == "error":
			// Statut HTTP 200 : c'est le traitement du site qui a échoué chez
			// Netlify, d'où le code « upstream ».
			return upstreamError("netlify", status, cmp.Or(d.ErrorMessage, "le déploiement a échoué"))
		}
		if time.Now().Add(netlifyPollInterval).After(deadline) {
			return nil
		}
		timer.Reset(netlifyPollInterval)
	}
}

func (n *Netlify) call(ctx context.Context, token, method, p, contentType string, body []byte, out any) (int, error) {
	status, data, err := send(ctx, n.HTTP, "netlify", method, endpoint(n.BaseURL, netlifyDefaultBaseURL, p), token, contentType, body)
	if err != nil {
		return status, err
	}
	if status < 200 || status > 299 {
		return status, &Error{Provider: "netlify", Status: status, Code: codeForStatus(status), Message: netlifyMessage(status, data)}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return status, upstreamError("netlify", status, "réponse illisible")
	}
	return status, nil
}

// netlifyMessage : Netlify répond {"message": …} ou {"code": …, "message": …},
// et {"errors": {champ: […]}} sur les 422 de validation.
func netlifyMessage(status int, body []byte) string {
	var e struct {
		Message string          `json:"message"`
		Error   string          `json:"error"`
		Errors  json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(body, &e) == nil {
		if m := cmp.Or(e.Message, e.Error); m != "" {
			return m
		}
		if len(e.Errors) > 0 && string(e.Errors) != "null" {
			return snippet(status, e.Errors)
		}
	}
	return snippet(status, body)
}

// normalizeNetlifySite : tolère ce qu'un utilisateur colle réellement
// (« https://mon-site.netlify.app/ ») et refuse tout ce qui pourrait sortir
// du segment d'URL prévu.
func normalizeNetlifySite(site string) (string, error) {
	site = strings.TrimSpace(site)
	site = strings.TrimPrefix(strings.TrimPrefix(site, "https://"), "http://")
	site = strings.TrimRight(site, "/")
	if site == "" {
		return "", nil
	}
	if !netlifySiteRe.MatchString(site) {
		return "", invalidf("identifiant de site Netlify invalide : %q", site)
	}
	return site, nil
}

// buildZip : archive en mémoire, entrées triées par chemin et sans horodatage
// — deux publications du même contenu produisent le même zip, octet pour octet.
func buildZip(files []File) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range slices.SortedFunc(slices.Values(files), byPath) {
		w, err := zw.Create(f.Path)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
