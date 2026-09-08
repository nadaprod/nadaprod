
const { useState, useEffect, useRef } = React;

const BOB_BLACK_ID = "bob_black_abolition";
const LAFARGUE_ID = "lafargue_paresse";

const SERVER_LIBRARY = [
	{
		id: BOB_BLACK_ID,
		title: "L'Abolition du Travail",
		subtitle: "Nul ne devrait jamais travailler",
		author: "Bob Black",
		year: "1985",
		desc: "Le travail est la source de toute misère. Bob Black appelle à une révolution ludique : transformer la production en jeu, en fête, en aventure collective.",
		theme_color: "border-red", 
		url: "/public/dl/bob-black-l-abolition-du-travail.epub", // Mock path
		coverColor: "#D32F2F",
		featured: true
	},
	{
		id: LAFARGUE_ID,
		title: "Le Droit à la Paresse",
		author: "Paul Lafargue",
		year: "1880",
		desc: "Une charge féroce contre l'amour du travail, cette 'folie furibonde' qui mène à l'épuisement. La base de la critique de l'aliénation.",
		theme_color: "border-etched", 
		url: "/public/dl/pamphlets_socialistes.epub",
		coverColor: "#C5A059"
	},
	{
		id: "bk_arch_01",
		title: "La Conquête du Pain",
		author: "Pierre Kropotkine",
		year: "1892",
		desc: "Comment organiser la production sans État ? Kropotkine répond : le bien-être pour tous est possible par l'entraide.",
		theme_color: "border-white",
		url: "/public/dl/pierre-kropotkine-la-conquete-du-pain.epub",
		coverColor: "#4B5563"
	}
];

const Icon = ({ name, size = 20, className = "" }) => {
	const ref = useRef(null);
	useEffect(() => {
		if (!ref.current || typeof lucide === 'undefined') return;
		const iconName = name.split('-').map(part => part.charAt(0).toUpperCase() + part.slice(1)).join('');
		const iconNode = lucide.icons[iconName];
		if (iconNode) {
			const svg = lucide.createElement(iconNode);
			svg.setAttribute('width', size);
			svg.setAttribute('height', size);
			svg.setAttribute('class', className);
			ref.current.innerHTML = '';
			ref.current.appendChild(svg);
		}
	}, [name, size, className]);
	return <span ref={ref} className="inline-flex shrink-0 items-center justify-center"></span>;
};

const Nav = ({ onLibrary, onHome }) => (
	<nav className="fixed top-0 left-0 w-full p-4 md:p-6 flex justify-between items-center z-50 pointer-events-none">
		<div onClick={onHome} className="pointer-events-auto cursor-pointer bg-revo-black border-2 border-revo-paper px-3 py-1 shadow-paper-white">
			<span className="font-wood text-xl tracking-tighter text-revo-red">NADA</span>
			<span className="font-wood text-xl tracking-tighter text-revo-paper">BOOKS</span>
		</div>
		<button onClick={onLibrary} className="pointer-events-auto btn-pamphlet px-4 py-2 text-xs md:text-sm bg-revo-white dark:bg-revo-black dark:text-[#F5F1E6] dark:border-[#F5F1E6]">
			Bibliothèque
		</button>
	</nav>
);

const CoconLanding = ({ onRead, hasProgress }) => {
	return (
		<div className="min-h-screen w-full flex flex-col items-center justify-center relative overflow-hidden py-20 px-4 dark:bg-revo-black dark:text-revo-paper">
			
			{/* Dark Propaganda Graphics */}
			<div className="absolute top-0 left-0 w-full h-full opacity-20 pointer-events-none z-0">
				<div className="absolute top-[15%] left-[5%] text-7xl md:text-9xl font-wood text-revo-paper opacity-10 rotate-12">LUDIQUE</div>
				<div className="absolute bottom-[20%] right-[10%] text-7xl md:text-[12rem] font-wood text-revo-red opacity-10 -rotate-12 uppercase leading-none">Abolition</div>
				<div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 opacity-5">
						<Icon name="dices" size={500} className="animate-pulse" />
				</div>
			</div>

			<div className="z-10 max-w-5xl w-full text-center space-y-10">
				<div className="space-y-4">
					<p className="font-mono text-revo-red uppercase tracking-[0.2em] text-sm md:text-base font-bold">Sortie du Samedi — Édition N°3</p>
					<h1 className="text-4xl sm:text-5xl md:text-7xl lg:text-8xl font-wood leading-[0.85] uppercase">
						L'Abolition<br/>
						<span className="text-revo-red">du travail</span>
					</h1>
					<p className="font-wood text-md sm:text-xl md:text-2xl lg:text-3xl tracking-wide text-revo-red uppercase">Nul ne devrait jamais travailler</p>
				</div>

				<div className="max-w-3xl mx-auto bg-revo-paper text-revo-black p-8 -rotate-1 shadow-paper-red border-4 border-revo-red">
					<p className="font-body text-xl md:text-2xl italic leading-relaxed font-semibold">
						"J'aspire au plein-chômage, comme les surréalistes - sauf que je ne plaisante pas, moi. Ma cause est celle de la <span className="text-revo-red underline">fête permanente</span>."
					</p>
					<p className="text-right mt-4 font-wood text-sm uppercase">— Bob Black, 1985</p>
				</div>

				<div className="flex flex-col md:flex-row gap-6 justify-center items-center pt-8">
					<button onClick={onRead} className="btn-primary px-10 py-5 text-xl md:text-3xl w-full md:w-auto flex items-center justify-center gap-4">
						<Icon name={hasProgress ? "play" : "flame"} /> {hasProgress ? "Reprendre la lecture" : "Lancer la lecture"}
					</button>
					<div className="text-xs font-mono uppercase tracking-tighter opacity-60 max-w-[200px] text-left border-l-2 border-revo-red pl-4">
						Misère du salariat, esclavage volontaire, produire, pourrir, mourir.
					</div>
				</div>
			</div>
			
			<div className="hidden lg:block absolute bottom-6 flex gap-8 opacity-40 font-mono text-[10px] uppercase tracking-[0.2em]">
				<span>Play for real</span>
				<span>Destroy the job</span>
				<span>NADAPROD BOOKS</span>
			</div>
		</div>
	);
};

const EpubReader = ({ bookData, bookId, bookTitle, onExit }) => {
	const viewerRef = useRef(null);
	const renditionRef = useRef(null);
	const [isReady, setIsReady] = useState(false);
	const [settings, setSettings] = useState(() => {
		const saved = localStorage.getItem('ca_settings');
		return saved ? JSON.parse(saved) : { theme: 'dark', fontSize: 22, fontFamily: "'Crimson Text', serif" };
	});

	useEffect(() => localStorage.setItem('ca_settings', JSON.stringify(settings)), [settings]);

	useEffect(() => {
		if (!bookData || typeof window.ePub === 'undefined') return;
		
		const book = window.ePub(bookData);
		const rendition = book.renderTo(viewerRef.current, { 
			width: "100%", 
			height: "100%", 
			flow: "paginated",
			allowScriptedContent: true 
		});
		renditionRef.current = rendition;

		rendition.hooks.content.register((contents) => {
			const doc = contents.document;
			const link = doc.createElement('link');
			link.setAttribute('rel', 'stylesheet');
			link.setAttribute('href', 'https://fonts.googleapis.com/css2?family=Bevan&family=Crimson+Text:ital,wght@0,400;0,600;0,700;1,400&family=Inter:wght@300;400;500;600;700;800&family=Syne:wght@400;500;600;700;800&family=Schoolbell&display=swap');
			doc.head.appendChild(link);
		});

		rendition.themes.register('paper', { 
			body: { color: '#1A1A1A', background: '#F5F1E6', 'font-family': "'Crimson Text', serif" },
			'h1, h2, h3': { 'font-family': "'Bevan', serif", color: '#D32F2F', 'text-transform': 'uppercase' }
		});
		rendition.themes.register('dark', { 
			body: { color: '#F5F1E6', background: '#1A1A1A', 'font-family': "'Crimson Text', serif" },
			'h1, h2, h3': { color: '#D32F2F', 'font-family': "'Bevan', serif", 'text-transform': 'uppercase' }
		});

		const savedLocation = localStorage.getItem(`ca_progress_${bookId}`);
		rendition.display(savedLocation || undefined).then(() => {
			setIsReady(true);
			rendition.themes.select(settings.theme);
			rendition.themes.fontSize(`${settings.fontSize}px`);
		});

		rendition.on('relocated', (location) => {
			localStorage.setItem(`ca_progress_${bookId}`, location.start.cfi);
		});

		const handleKey = (e) => {
			if (e.key === "ArrowLeft") rendition.prev();
			if (e.key === "ArrowRight") rendition.next();
			if (e.key === "Escape") onExit();
		};
		window.addEventListener('keydown', handleKey);
		return () => {
			book.destroy();
			window.removeEventListener('keydown', handleKey);
		};
	}, [bookData, bookId]);

	useEffect(() => {
		if (renditionRef.current) {
			const r = renditionRef.current;
			if (!r) return;

			// setup
			r.themes.select(settings.theme);
			r.themes.fontSize(`${settings.fontSize}px`);

			// refresh
			r.manager?.updateLayout();
			r.views()?.forEach(v => v.pane?.render());
		}
	}, [settings]);
	
	useEffect(() => {
		document.documentElement.classList.toggle(
			'dark',
			settings.theme === 'dark'
		);
	}, [settings.theme]);


	const isDark = settings.theme === 'dark';

	return (
		<div className={`fixed inset-0 z-[100] flex flex-col h-full w-full ${isDark ? "bg-revo-black" : "bg-revo-paper"}`}>
			<div className={`h-14 border-b-2 flex items-center justify-between px-4 shrink-0 ${isDark ? "border-white/10" : "border-black"}`}>
				<button onClick={onExit} className="font-wood hover:text-revo-red transition-colors flex items-center gap-2 bg-revo-white dark:bg-revo-black dark:text-[#F5F1E6] dark:border-[#F5F1E6]">
					<Icon name="arrow-left" /> <span className="hidden sm:inline">Quitter</span>
				</button>
				<h1 className="font-wood text-sm md:text-lg uppercase truncate px-4 bg-revo-white dark:bg-revo-black dark:text-[#F5F1E6] dark:border-[#F5F1E6]">{bookTitle}</h1>
				<div className="flex gap-2 bg-revo-white dark:bg-revo-black dark:text-[#F5F1E6] dark:border-[#F5F1E6]">
						<button onClick={() => setSettings(s => ({...s, theme: s.theme === 'paper' ? 'dark' : 'paper'}))} className="p-2 border border-current hover:bg-revo-red text-black hover:text-white transition-all">
						{isDark ? <Icon name="sun" size={16}/> : <Icon name="moon" size={16}/>}
					</button>
					<button onClick={() => {
						const sizes = [16, 20, 24, 30];
						const next = sizes[(sizes.indexOf(settings.fontSize) + 1) % sizes.length];
						setSettings(s => ({...s, fontSize: next}));
					}} className="px-3 py-1 border border-current font-mono text-xs hover:bg-revo-red text-black hover:text-white transition-all">
						{settings.fontSize}px
					</button>
				</div>
			</div>
			<div className="flex-1 relative">
				{!isReady && <div className="absolute inset-0 flex items-center justify-center bg-inherit z-50"><Icon name="loader" className="animate-spin text-revo-red" size={48} /></div>}
				<div ref={viewerRef} className="h-full w-full"></div>
				<button onClick={() => renditionRef.current?.prev()} className="absolute left-0 bottom-4 md:bottom-0 md:h-full w-12 hover:bg-black/5 flex items-center justify-center group bg-revo-white dark:bg-revo-black dark:text-[#F5F1E6] dark:border-[#F5F1E6]"><Icon name="chevron-left" className="animate-pulse opacity-50 group-hover:opacity-100" /></button>
				<button onClick={() => renditionRef.current?.next()} className="absolute right-0 bottom-4 md:bottom-0 md:h-full w-12 hover:bg-black/5 flex items-center justify-center group bg-revo-white dark:bg-revo-black dark:text-[#F5F1E6] dark:border-[#F5F1E6]"><Icon name="chevron-right" className="animate-pulse opacity-50 group-hover:opacity-100" /></button>
			</div>
		</div>
	);
};

const App = () => {
	const [view, setView] = useState('home');
	const [currentBook, setCurrentBook] = useState(null);
	const [progress, setProgress] = useState(false);
	const [theme, setTheme] = useState(() => {
		return localStorage.getItem('ca_theme') || 'dark';
	});

	useEffect(() => {
		localStorage.setItem('ca_theme', theme);
		document.documentElement.classList.toggle('dark', theme === 'dark');
	}, [theme]);

	useEffect(() => {
		const check = () => setProgress(!!localStorage.getItem(`ca_progress_${BOB_BLACK_ID}`));
		check();
		const itv = setInterval(check, 2000);
		return () => clearInterval(itv);
	}, []);

	useEffect(() => {
	    const handler = () => {
		const h = window.location.hash.slice(1);
		const book = SERVER_LIBRARY.find(b => b.id === h);
		if (book) openBook(book);
	    };
	    window.addEventListener("hashchange", handler);
	    handler();
	    return () => window.removeEventListener("hashchange", handler);
	}, []);

	const openBook = (book) => {
		setCurrentBook({ id: book.id, title: book.title, url: book.url });
		setView('reader');
	};

	const handleFileUpload = (file) => {
		const reader = new FileReader();
		reader.onload = (e) => {
			setCurrentBook({ id: "local_" + Date.now(), title: file.name, data: e.target.result });
			setView('reader');
		};
		reader.readAsArrayBuffer(file);
	};

	return (
		<div className="h-full w-full">
			{view !== 'reader' && <Nav onLibrary={() => setView('library')} onHome={() => setView('home')} />}

			{view === 'home' && <CoconLanding theme={theme} onRead={() => openBook(SERVER_LIBRARY[0])} hasProgress={progress} />}

			{view === 'library' && (
				<div className="pt-24 px-4 max-w-6xl mx-auto pb-20">
					<h2 className="font-wood text-4xl mb-12 border-b-8 border-revo-red inline-block">DISPONIBLES</h2>
					<div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-10">
						{SERVER_LIBRARY.map(book => (
							<div key={book.id} className="bg-revo-paper text-revo-black border-4 border-revo-black shadow-paper-white p-6 flex flex-col gap-4 group">
								<div className="border-b-2 border-black pb-4">
									<h3 className="font-wood text-3xl leading-tight group-hover:text-revo-red transition-colors">{book.title}</h3>
									<p className="font-mono text-sm opacity-60 mt-1">{book.author}, {book.year}</p>
								</div>
								<p className="font-body italic text-lg leading-snug line-clamp-3">{book.desc}</p>
								<button onClick={() => openBook(book)} className="btn-primary py-3 bg-white hover:bg-revo-red text-black hover:text-white border-4 border-black text-black">
									<Icon name={book.hasProgress ? "play" : "flame"} /> {book.hasProgress ? "Reprendre la lecture" : "Lancer la lecture"}
								</button>
							</div>
						))}
						<label className="cursor-pointer bg-revo-red text-white border-4 border-white shadow-paper flex flex-col items-center justify-center gap-4 p-6 min-h-[250px] hover:scale-[1.02] transition-transform">
							<Icon name="upload" size={48} />
							<span className="font-wood text-2xl uppercase text-center">Lire un .ePub</span>
							<input type="file" accept=".epub" className="hidden" onChange={(e) => e.target.files[0] && handleFileUpload(e.target.files[0])} />
						</label>
					</div>
				</div>
			)}

			{view === 'reader' && currentBook && (
				<EpubReader theme={theme} setTheme={setTheme}
					bookData={currentBook.url || currentBook.data} 
					bookId={currentBook.id}
					bookTitle={currentBook.title} 
					onExit={() => setView('home')} 
				/>
			)}
		</div>
	);
};

return App
