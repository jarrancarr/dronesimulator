# dronesimulator


Assuming redis is running, execute the following command:
./drone 
this will begin a new drone simulator that accepts rest commands from port 3000. With the following format:
{
	"path": "[sine|clockwise|figure8|patrol|counter-clockwise]",
	"start": { "lat": ###, "lon": ###}
	"end": { "lat": ###, "lon": ###},
	"points":[ { "lat": ###, "lon": ###}, { "lat": ###, "lon": ###}, { "lat": ###, "lon": ###}],
	"data":[#, #, #, #]
}

The progam will publish the drone data to redis with the key: "drone-<id>" and simultaneously publishes the same to
the web socket port 3070.  The drone monitor program will read out the values published to redis; and any web socket reader 
will output the results published to the drone output port.  

The following will fly a cicular pattern:
{
	"path": "clockwise",
	"start": { "lat": -33.937687, "lon": 151.19189864},
	"end": { "lat": -33.937887, "lon": 151.19189864},
    "points":[ { "lat": -33.937787, "lon": 151.19179864},{ "lat": -33.937787, "lon": 151.19199864}]
}
