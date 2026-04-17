/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./layouts/**/*.html",
    "./content/**/*.md",
    "./app/src/**/*.{ts,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        // Deep corporate navy — the primary brand colour. Used for text,
        // buttons, nav, and hero backgrounds.
        navy: {
          50: "#f2f4f8",
          100: "#e4e7ef",
          200: "#c4cadd",
          300: "#8e97b5",
          400: "#616e96",
          500: "#3f4b79",
          600: "#2c365d",
          700: "#1f2747",
          800: "#141a33",
          900: "#0c1226",
          950: "#050912",
        },
        // Mustard/amber — used sparingly for highlights, small accents,
        // and the occasional "priority" affordance. Not a button colour.
        accent: {
          50: "#fffbeb",
          100: "#fef3c7",
          200: "#fde68a",
          300: "#fcd34d",
          400: "#fbbf24",
          500: "#f59e0b",
          600: "#d97706",
          700: "#b45309",
          800: "#92400e",
          900: "#78350f",
        },
      },
      fontFamily: {
        sans: ["Inter", "system-ui", "-apple-system", "sans-serif"],
      },
    },
  },
  plugins: [
    require("@tailwindcss/forms"),
    require("@tailwindcss/typography"),
  ],
};
