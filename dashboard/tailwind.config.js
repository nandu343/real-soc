module.exports = {
  content: [
    "./pages/**/*.{js,ts,jsx,tsx}",
    "./components/**/*.{js,ts,jsx,tsx}"
  ],
  theme: {
    extend: {
      colors: {
        primary: {
          DEFAULT: '#7BD389',
          50: '#F6FEF6',
          100: '#E8FFF0'
        },
        accent: {
          DEFAULT: '#FBBF24'
        },
        surface: '#FEFBF7',
        muted: '#6B7280',
        danger: '#EF4444',
        success: '#10B981'
      },
      boxShadow: {
        soft: '0 10px 30px rgba(15,23,42,0.06)',
        inner: 'inset 0 1px 0 rgba(255,255,255,0.6)'
      },
      fontFamily: {
        sans: ['Inter', 'ui-sans-serif', 'system-ui', '-apple-system', 'Segoe UI', 'Roboto', 'Helvetica Neue', 'Arial']
      }
    }
  },
  plugins: []
}
