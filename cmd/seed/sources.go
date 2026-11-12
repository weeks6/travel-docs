package main

// Sample routes from https://github.com/gps-touring/sample-gpx: cycle
// touring tracks from Brittany to the Jura and along the Breton coast. The
// repository has no license, so the files are downloaded when seeding
// rather than copied into this repository. They are pinned to one commit so
// the seeded data doesn't change underneath us.

const (
	sourceRepo   = "gps-touring/sample-gpx"
	sourceCommit = "0635f91541d9ba210ee8a0f50952613623537824"
)

// sample is one GPX file to import.
type sample struct {
	Path string // in the repository
	Name string // route name to give it
}

// group becomes a collection of its samples.
type group struct {
	Collection  string
	Description string
	Samples     []sample
}

// Only the full tracks: the repository also has simplified copies of them
// (SimplifiedGpx/, simplified/) and waypoint-only files (Campsites/, poi/),
// which aren't routes.
var groups = []group{
	{
		Collection:  "Brittany to the Jura",
		Description: "Cycle touring between the Channel, Brittany and the Swiss Jura, including EuroVelo 6.",
		Samples: []sample{
			{"BrittanyJura/Basel_St-Brevin_Eurovelo6.gpx", "EuroVelo 6: Basel – Saint-Brevin"},
			{"BrittanyJura/Alt_Portsmouth.gpx", "Portsmouth (alternative)"},
			{"BrittanyJura/Southampton_Portsmouth.gpx", "Southampton – Portsmouth"},
			{"BrittanyJura/Newhaven_Brighton.gpx", "Newhaven – Brighton"},
			{"BrittanyJura/Ouistreham_Caen.gpx", "Ouistreham – Caen"},
			{"BrittanyJura/Serqueaux_Dieppe.gpx", "Serqueux – Dieppe"},
			{"BrittanyJura/Reims-VitryLeFrancois.gpx", "Reims – Vitry-le-François"},
			{"BrittanyJura/Vitry-le-Francois_Langres.gpx", "Vitry-le-François – Langres"},
			{"BrittanyJura/Dole_Langres.gpx", "Dole – Langres"},
			{"BrittanyJura/Dole_Salin-les-Bains.gpx", "Dole – Salins-les-Bains"},
			{"BrittanyJura/Salins-les-Bains_Fleurier.gpx", "Salins-les-Bains – Fleurier"},
			{"BrittanyJura/JuraRoute72011.gpx", "Jura Route 7"},
			{"BrittanyJura/Courgenay_Ballon-DAlsace.gpx", "Courgenay – Ballon d'Alsace"},
			{"BrittanyJura/VoieVerteHauteVosges.gpx", "Voie Verte des Hautes-Vosges"},
			{"BrittanyJura/MoselradwegAusWiki.gpx", "Moselle cycle path"},
		},
	},
	{
		Collection:  "Breton coast from Roscoff",
		Description: "Coastal stages in north Brittany between Roscoff, Morlaix and Perros-Guirec.",
		Samples: []sample{
			{"RoscoffCoastal/Roscoff_Perros-Guirec.gpx", "Roscoff – Perros-Guirec"},
			{"RoscoffCoastal/1_Roscoff_Morlaix_A_parcours.gpx", "Roscoff – Morlaix"},
			{"RoscoffCoastal/parcours-morlaix-plougasnou.gpx", "Morlaix – Plougasnou"},
			{"RoscoffCoastal/Plougasou-plestin-parcours.gpx", "Plougasnou – Plestin-les-Grèves"},
			{"RoscoffCoastal/Lannion_Plestin_parcours24.4RE.gpx", "Lannion – Plestin-les-Grèves"},
			{"RoscoffCoastal/Trebeurden_Lannion_parcours13.2RE.gpx", "Trébeurden – Lannion"},
			{"RoscoffCoastal/Perros-Guirec_Trebeurden_parcours23.6RE.gpx", "Perros-Guirec – Trébeurden"},
		},
	},
}
