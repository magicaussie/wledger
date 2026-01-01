package handler

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/components"
	"github.com/tuxedocurly/wledger/web/pages"
)

func (h *Handler) HandleWallCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	desc := r.FormValue("description")

	if name == "" {
		h.UIError.Respond(w, r, nil, "Name is required", http.StatusBadRequest)
		return
	}

	_, err := h.Dashboard.CreateWall(r.Context(), name, desc)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to create wall", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) HandleWallEdit(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	ctx := r.Context()
	wallID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	// Fetch Wall
	walls, _ := h.Dashboard.GetWalls(ctx)
	var wall db.Wall
	found := false
	for _, w := range walls {
		if w.ID == wallID {
			wall = w
			found = true
			break
		}
	}
	if !found {
		h.UIError.Respond(w, r, nil, "Wall not found", http.StatusNotFound)
		return
	}

	// Fetch Wall Containers
	wallContainers, err := h.Dashboard.GetWallWithContainers(ctx, wallID)
	if err != nil {
		wallContainers = []components.DashboardContainer{}
	}

	// Fetch All Containers
	allContainers, _ := h.Queries.GetAllContainers(ctx)
	if allContainers == nil {
		allContainers = []db.Container{}
	}

	pages.WallEdit(user, wall, wallContainers, allContainers).Render(ctx, w)
}

func (h *Handler) HandleWallUpdate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	name := r.FormValue("name")
	desc := r.FormValue("description")
	containerIDsRaw := r.PostForm["container_ids[]"]

	h.Logger.Info("HandleWallUpdate", "wall_id", id, "container_ids", containerIDsRaw)

	var containerIDs []int64
	for _, s := range containerIDsRaw {
		cid, _ := strconv.ParseInt(s, 10, 64)
		if cid != 0 {
			containerIDs = append(containerIDs, cid)
		}
	}

	err := h.Dashboard.UpdateWall(r.Context(), id, name, desc, containerIDs)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to update wall", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) HandleWallDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	err := h.Dashboard.DeleteWall(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to delete wall", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}