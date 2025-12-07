module.exports = {
  content: ["./web/**/*.html", "./web/**/*.templ"],
  theme: {
    extend: {},
  },
  plugins: [require("daisyui")],
  daisyui: {
    themes: ["light", "dark"]
  }
}
