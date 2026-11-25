package com.zqmxm.omni;

import android.app.Activity;
import android.os.Bundle;
import android.view.Gravity;
import android.widget.TextView;

/** 最小可运行页面：不依赖任何布局 XML 或第三方库。 */
public class MainActivity extends Activity {

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        TextView view = new TextView(this);
        view.setText("Hello from omni-repo\n任何 AI 都能接着写这个 App");
        view.setGravity(Gravity.CENTER);
        view.setTextSize(20);
        setContentView(view);
    }
}
