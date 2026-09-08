export default {
	darkMode: 'class',
	content: ['./index.html', './src/**/*.{js,jsx}'],
	theme: {
		extend: {
			fontFamily: {
				sans: ['Inter', 'sans-serif'],
				display: ['"Bevan"', 'serif'], 
				body: ['"Crimson Text"', 'serif'],
			},
			colors: {
				revo: {
					red: '#D32F2F',      
					black: '#1A1A1A',    
					paper: '#F5F1E6',    
					ink: '#2C2C2C',      
					gold: '#C5A059'      
				}
			},
			boxShadow: {
				'paper': '4px 4px 0px #1A1A1A',
				'paper-red': '4px 4px 0px #D32F2F',
				'paper-white': '4px 4px 0px #F5F1E6',
			},
			animation: {
				'float': 'float 6s ease-in-out infinite',
				'stamp': 'stamp 0.3s cubic-bezier(0.175, 0.885, 0.32, 1.275) forwards',
			},
			keyframes: {
				float: {
					'0%, 100%': { transform: 'translateY(0)' },
					'50%': { transform: 'translateY(-10px)' },
				},
				stamp: {
					'0%': { opacity: 0, transform: 'scale(2) rotate(-10deg)' },
					'100%': { opacity: 1, transform: 'scale(1) rotate(-2deg)' }
				}
			}
		}
	},
	plugins: []
}
