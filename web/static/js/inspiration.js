document.addEventListener('alpine:init', () => {
    Alpine.data('inspirationApp', () => ({
        selectedTags: [],
        toggleTag(tag) {
            if (this.selectedTags.includes(tag)) {
                this.selectedTags = this.selectedTags.filter(t => t !== tag);
            } else {
                this.selectedTags.push(tag);
            }
        },
        copyPrompt(id) {
            const tagsQuery = this.selectedTags.length > 0 ? '?tags=' + this.selectedTags.join(',') : '';
            fetch('/inspiration/' + id + '/generate' + tagsQuery)
                .then(response => response.text())
                .then(text => {
                    navigator.clipboard.writeText(text).then(() => {
                        const toast = document.createElement('div');
                        toast.className = 'toast toast-top toast-center z-50';
                        toast.innerHTML = '<div class="alert alert-success text-white"><span>Copied to clipboard!</span></div>';
                        document.body.appendChild(toast);
                        setTimeout(() => toast.remove(), 2000);
                    });
                })
                .catch(err => {
                    console.error('Failed to copy: ', err);
                    alert('Failed to generate prompt. See console.');
                });
        }
    }))
})

