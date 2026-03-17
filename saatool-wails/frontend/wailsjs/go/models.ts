export namespace main {
	
	export class BookDetailsInfo {
	    title: string;
	    author: string;
	    genre: string;
	    synopsis: string;
	    writingStyle: string;
	    characters: translation.Character[];
	
	    static createFrom(source: any = {}) {
	        return new BookDetailsInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.author = source["author"];
	        this.genre = source["genre"];
	        this.synopsis = source["synopsis"];
	        this.writingStyle = source["writingStyle"];
	        this.characters = this.convertValues(source["characters"], translation.Character);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ParagraphInfo {
	    index: number;
	    text: string;
	    isSource: boolean;
	    direction: string;
	    total: number;
	    chapterStart: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ParagraphInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.text = source["text"];
	        this.isSource = source["isSource"];
	        this.direction = source["direction"];
	        this.total = source["total"];
	        this.chapterStart = source["chapterStart"];
	    }
	}
	export class Position {
	    index: number;
	    sourceView: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Position(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.sourceView = source["sourceView"];
	    }
	}
	export class ProjectInfo {
	    path: string;
	    name: string;
	    title: string;
	    author: string;
	    genre: string;
	    writingStyle: string;
	    sourceLang: string;
	    targetLang: string;
	    total: number;
	    translated: number;
	    readAt: number;
	    direct: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProjectInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.title = source["title"];
	        this.author = source["author"];
	        this.genre = source["genre"];
	        this.writingStyle = source["writingStyle"];
	        this.sourceLang = source["sourceLang"];
	        this.targetLang = source["targetLang"];
	        this.total = source["total"];
	        this.translated = source["translated"];
	        this.readAt = source["readAt"];
	        this.direct = source["direct"];
	    }
	}
	export class Settings {
	    deepSeekAPIKey: string;
	    translateAhead: number;
	    appSize: number;
	    translationDocSize: number;
	    autoProofread: boolean;
	    sourceLanguage: string;
	    targetLanguage: string;
	    darkMode: boolean;
	    fixModel: string;
	    maxConcurrentTranslations: number;
	    translationBatchSize: number;
	    projectsDirectory: string;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.deepSeekAPIKey = source["deepSeekAPIKey"];
	        this.translateAhead = source["translateAhead"];
	        this.appSize = source["appSize"];
	        this.translationDocSize = source["translationDocSize"];
	        this.autoProofread = source["autoProofread"];
	        this.sourceLanguage = source["sourceLanguage"];
	        this.targetLanguage = source["targetLanguage"];
	        this.darkMode = source["darkMode"];
	        this.fixModel = source["fixModel"];
	        this.maxConcurrentTranslations = source["maxConcurrentTranslations"];
	        this.translationBatchSize = source["translationBatchSize"];
	        this.projectsDirectory = source["projectsDirectory"];
	    }
	}

}

export namespace translation {
	
	export class Bookmark {
	    index: number;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new Bookmark(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.note = source["note"];
	    }
	}
	export class Character {
	    name: string;
	    gender?: string;
	    age?: number;
	    role?: string;
	    description?: string;
	
	    static createFrom(source: any = {}) {
	        return new Character(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.gender = source["gender"];
	        this.age = source["age"];
	        this.role = source["role"];
	        this.description = source["description"];
	    }
	}

}

