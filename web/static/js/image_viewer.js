// Global helper to open the image zoom dialogs rendered by
// components.ImageZoomModal. Usage: window.openImage('part-img-123').
window.openImage = function (dialogId) {
    const el = document.getElementById(dialogId);
    if (el) el.showModal();
};